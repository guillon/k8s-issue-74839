package main

import (
	"os"
	"log"
	"net"
	"time"
	"strconv"
)

func main() {
	var port int
	port = 9000
	if len(os.Args) > 1 {
		p, err := strconv.Atoi(os.Args[1])
		if err != nil {
			panic(err)
		}
		port = p
	}
	ip := getIP().String()
	log.Printf("external ip: %v", ip)

	go probe(ip, port)

	log.Printf("listen on %v:%v", "0.0.0.0", port)

	listener, err := net.Listen("tcp", "0.0.0.0:9000")
	if err != nil {
		panic(err)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			panic(err)
		}
		log.Printf("Accepted connection: %+v", conn)

		go func(conn net.Conn) {
			time.Sleep(10 * time.Second)
			conn.Close()
		}(conn)
	}
}

func probe(ip string, port int) {
	log.Printf("probing %v", ip)

	ipAddr, err := net.ResolveIPAddr("ip4:tcp", ip)
	if err != nil {
		panic(err)
	}
	
	log.Printf("IP Addr: %v", ipAddr)
	conn, err := net.ListenIP("ip4:tcp", ipAddr)
	if err != nil {
		panic(err)
	}

	pending := make(map[string]uint32)
	established := make(map[string]bool)

	var buffer [4096]byte
	for {
		n, addr, err := conn.ReadFrom(buffer[:])
		if err != nil {
			log.Printf("conn.ReadFrom() error: %v", err)
			continue
		}

		pkt := &tcpPacket{}
		data, err := pkt.decode(buffer[:n])
		if err != nil {
			log.Printf("tcp packet parse error: %v", err)
			continue
		}
		remoteIP := net.ParseIP(addr.String())
		localIP := net.ParseIP(conn.LocalAddr().String())
		connect_id := localIP.String() + ":" + strconv.Itoa(int(pkt.DestPort)) + "-" + remoteIP.String() + ":" + strconv.Itoa(int(pkt.SrcPort))

		if localIP.String() != ipAddr.String() || int(pkt.DestPort) != port {
			continue
		}

		log.Printf("conn %v: tcp packet: %+v, flag: %v, data: %v, addr: %v", connect_id, pkt, pkt.FlagString(), data, addr)

		if pkt.Flags&SYN != 0 {
			pending[connect_id] = pkt.Seq + 1
			continue
		}
		if pkt.Flags&RST != 0 {
			if established[connect_id] {
				log.Printf("conn %v: RST received", connect_id)
				panic("RST received")
			}
		}
		if pkt.Flags&FIN != 0 {
			if established[connect_id] {
				log.Printf("conn %v: normal temination", connect_id)
				delete(established, connect_id)
			}
		}
		if pkt.Flags&ACK != 0 {
			if seq, ok := pending[connect_id]; ok {
				log.Printf("conn %v: ACK connection established", connect_id)
				delete(pending, connect_id)
				established[connect_id] = true
				n_packets := 1
				data, size := []byte("boom!!!!\n"), 9
				base := pkt.Ack
				num_rounds := 1
				overflow := 100000
				for j := 0; j < num_rounds; j++ {
					for i := 0; i < n_packets; i++ {
						bad_seq := uint32((uint64(base) + uint64(overflow) + uint64(i) * uint64(size)) % ((uint64(2)<<32)))
						badPkt := &tcpPacket{
							SrcPort:    pkt.DestPort,
							DestPort:   pkt.SrcPort,
							Ack:        seq,
							Seq:        bad_seq,      // Bad: seq out-of-window
							Flags:      (5 << 12) | PSH | ACK, // Offset and Flags  oooo000F FFFFFFFF (o:offset, F:flags)
							WindowSize: pkt.WindowSize,
						}
						
						_, err := conn.WriteTo(badPkt.encode(localIP, remoteIP, data[:]), addr)
						if err != nil {
							log.Printf("conn.WriteTo() error: %v", err)
						}
					}
				}
				log.Printf("conn %v: wrote %v invalid packets with seq: [seq + %v , seq + %v + %v[ (seq %v)", connect_id, n_packets, overflow, overflow, n_packets * size, base)
			}
		}
	}
}

func getIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	log.Printf("Local Address: %v", localAddr)
	return localAddr.IP
}
