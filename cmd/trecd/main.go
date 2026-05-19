package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"gitub.com/thorsager/trec/internal/b2bua"
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

	h := b2bua.NewHandler(b2bua.Config{
		Registrar:      reg,
		SessionManager: media.NewSessionManager(),
		Server:         server,
		ServerIP:       serverIP,
		ServerAddr:     flagAddr,
		UACManager:     sip.NewUACManager(),
		RTPPortMin:     flagRTPMin,
		RTPPortMax:     flagRTPMax,
	})

	server.On(proto.SIPMethodREGISTER, reg.HandleRegister)
	server.On(proto.SIPMethodOPTIONS, h.HandleOptions)
	server.On(proto.SIPMethodINVITE, h.HandleInvite)
	server.On(proto.SIPMethodBYE, h.HandleBye)
	server.OnAck(h.HandleAck)
	server.OnResponse(h.HandleResponse)

	server.Start()
	defer server.Close()

	log.Printf("T-REC SIP server listening on %s (UDP + TCP)", flagAddr)

	<-ctx.Done()
	log.Println("Shutting down...")
}
