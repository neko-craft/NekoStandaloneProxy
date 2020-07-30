package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"net"
	"regexp"
	"strings"
)

type Server struct {
	Address string `json:"address"`
	Regexp  string `json:"regexp"`
	addr    *net.TCPAddr
	regexp  *regexp.Regexp
	name    string
}

type Config struct {
	Deadline int                `json:"deadline"`
	Address  string             `json:"host"`
	Servers  map[string]*Server `json:"servers"`
}

func main() {
	REQUEST, PONG := []byte{1, 0}, []byte{9, 1, 0, 0, 0, 0, 0, 0, 0, 0}

	data, err := ioutil.ReadFile("./config.json")
	config := Config{
		30000,
		":25565",
		map[string]*Server{
			"default": {":25566", ".", nil, nil, "default"},
		},
	}
	if err != nil {
		data, err = json.MarshalIndent(&config, "", "  ")
		err = ioutil.WriteFile("./config.json", data, 0644)
		if err != nil {
			log.Panicln(err)
		}
	}
	err = json.Unmarshal(data, &config)
	if err != nil {
		log.Panicln(err)
	}

	notOnlyOne := len(config.Servers) > 1
	for key, server := range config.Servers {
		addr, err := net.ResolveTCPAddr("tcp", server.Address)
		if err != nil {
			log.Panicln(err)
		}
		reg, err := regexp.Compile(server.Regexp)
		if err != nil {
			log.Panicln(err)
		}
		server.addr = addr
		server.regexp = reg
		server.name = key
	}
	defaultServer, ok := config.Servers["default"]
	if !ok {
		log.Panicln(errors.New("no a feedback server named default"))
	}

	tcpAddr, err := net.ResolveTCPAddr("tcp", config.Address)
	if err != nil {
		log.Panicln(err)
	}
	listener, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		log.Panicln(err)
	}

	log.Println("Listening on:", config.Address)
	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			log.Println(err)
			continue
		}
		//err = conn.SetKeepAlive(true)
		if err != nil {
			continue
		}
		go func() {
			// noinspection GoUnhandledErrorResult
			defer conn.Close()
			arr := make([]byte, 5)
			_, err := conn.Read(arr)
			if err != nil || arr[0] == 254 || arr[1] != 0 ||
				arr[2] < 127 || // Protocol Version < 200 (> 1.9.*)
				arr[3] > 100 {
				return
			} // Server Address length > 127

			allLen := arr[0] - 4
			if allLen < 6 || allLen > 70 {
				return
			}

			received := make([]byte, allLen)
			_, err = conn.Read(received)
			if err != nil {
				return
			}

			state := received[allLen-1]
			switch state {
			case 1:
				data := make([]byte, 2)
				_, err = conn.Read(data)
				if err != nil || data[0] != 1 || data[1] != 0 {
					return
				}
				break
			case 2:
				break
			default:
				return
			}

			port := uint16(received[allLen-3])<<4 | uint16(received[allLen-2])
			if port > 65535 || port == 0 {
				return
			}

			address := string(received[0:(allLen - 3)])
			matched := defaultServer
			if notOnlyOne {
				for _, server := range config.Servers {
					if server.regexp.MatchString(address + ":" + string(port)) {
						matched = server
					}
				}
			}

			src := conn.RemoteAddr().String()
			if state == 2 {
				log.Println("New connection:", src, "->", matched.name)
			}

			remote, err := net.DialTCP("tcp", nil, matched.addr)
			if err != nil {
				log.Println(err)
				return
			}
			// noinspection GoUnhandledErrorResult
			defer remote.Close()
			switch state {
			case 1:
				_, err = remote.Write(arr)
				if err != nil {
					return
				}
				_, err = remote.Write(received)
				if err != nil {
					return
				}
				// noinspection GoUnhandledErrorResult
				_, err = remote.Write(REQUEST)
				if err != nil {
					return
				}
				arr := make([]byte, 1)
				_, err = remote.Read(arr)
				if err != nil {
					return
				}
				length := arr[0]
				arr0, arr1 := length, byte(0)
				if length > 127 {
					_, err = remote.Read(arr)
					if err != nil {
						return
					}
					arr1 = arr[0]
					length |= arr1 << 7
				}
				data := make([]byte, length)
				_, err = remote.Read(data)
				if err != nil {
					return
				}
				if arr1 == 0 {
					_, err = conn.Write(arr)
				} else {
					_, err = conn.Write([]byte{arr0, arr1})
				}
				if err != nil {
					return
				}
				_, _ = conn.Write(data)
				data = make([]byte, 10)
				_, err = conn.Read(data)
				if err != nil || data[0] != 9 || data[1] != 1 {
					return
				}
				_, _ = conn.Write(PONG)
				return
			case 2:
				address += "\x00"
				address += strings.Split(src, ":")[0]
				address += "\x0000000000-0000-0000-0000-000000000000"

				addressBytes := []byte(address)
				length := len(addressBytes)
				arr[0] = byte(7 + length)
				arr[4] = byte(length)
				_, err = remote.Write(arr)
				if err != nil {
					return
				}

				_, err = remote.Write(addressBytes)
				if err != nil {
					return
				}
				_, err = remote.Write([]byte{received[allLen-3], received[allLen-2], 2})
				if err != nil {
					return
				}

				// noinspection GoUnhandledErrorResult
				go io.Copy(conn, remote)
				_, err = io.Copy(remote, conn)
			}
		}()
	}
}
