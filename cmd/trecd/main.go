package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"gitub.com/thorsager/trec/internal/media"
	"gitub.com/thorsager/trec/internal/sip"
	"gitub.com/thorsager/trec/proto"
)

var (
	flagAddr   string
	flagRTPMin int
	flagRTPMax int
)

var serverIP = "127.0.0.1"

func init() {
	flag.StringVar(&flagAddr, "addr", ":5060", "SIP listen address")
	flag.IntVar(&flagRTPMin, "rtp-min", 0, "RTP port range start (0 = OS-assigned)")
	flag.IntVar(&flagRTPMax, "rtp-max", 0, "RTP port range end (0 = OS-assigned)")
	flag.Parse()
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if flagRTPMin > 0 && flagRTPMax > 0 && flagRTPMax >= flagRTPMin {
		media.RTPPortMin = flagRTPMin
		media.RTPPortMax = flagRTPMax
		log.Printf("RTP port range: %d-%d", flagRTPMin, flagRTPMax)
	} else {
		log.Printf("RTP port range: OS-assigned")
	}

	server, err := sip.NewServer(flagAddr)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	reg := sip.NewRegistrar()
	go reg.Start(ctx)

	server.SetFlowDeadCallback(reg.RemoveBindingsByFlowID)

	h := &ServerHandlers{
		reg:        reg,
		sm:         media.NewSessionManager(),
		server:     server,
		serverIP:   serverIP,
		serverAddr: flagAddr,
		uacMgr:     sip.NewUACManager(),
		b2buaCalls: make(map[string]*B2BUACall),
		b2buaByBob: make(map[string]string),
	}

	server.On(proto.SIPMethodREGISTER, reg.HandleRegister)
	server.On(proto.SIPMethodOPTIONS, h.handleOptions)
	server.On(proto.SIPMethodINVITE, h.handleInvite)
	server.On(proto.SIPMethodBYE, h.handleBye)
	server.OnAck(h.handleAck)
	server.OnResponse(h.handleResponse)

	server.Start()
	defer server.Close()

	log.Printf("T-REC SIP server listening on %s (UDP + TCP)", flagAddr)

	<-ctx.Done()
	log.Println("Shutting down...")
}
