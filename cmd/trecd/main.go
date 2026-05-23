package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/thorsager/trecs/internal/b2bua"
	"github.com/thorsager/trecs/internal/dialplan"
	"github.com/thorsager/trecs/internal/logutil"
	"github.com/thorsager/trecs/internal/media"
	"github.com/thorsager/trecs/internal/sip"
	"github.com/thorsager/trecs/proto"
)

var (
	flagAddr      string
	flagRTPMin    int
	flagRTPMax    int
	flagDialplan  string
	flagLogLevel  string
	flagLogFormat string
)

var serverIP = "127.0.0.1"

func init() {
	flag.StringVar(&flagAddr, "addr", ":5060", "SIP listen address")
	flag.IntVar(&flagRTPMin, "rtp-min", 0, "RTP port range start (0 = OS-assigned)")
	flag.IntVar(&flagRTPMax, "rtp-max", 0, "RTP port range end (0 = OS-assigned)")
	flag.StringVar(&flagDialplan, "dialplan", "", "Path to dialplan JSON file")
	flag.StringVar(&flagLogLevel, "log-level", "info", "Log level (trace, debug, info, warn, error)")
	flag.StringVar(&flagLogFormat, "log-format", "text", "Log format (text or json)")
	flag.Parse()
}

func main() {
	var lvl slog.Level
	switch flagLogLevel {
	case "trace":
		lvl = logutil.LevelTrace
	default:
		if err := lvl.UnmarshalText([]byte(flagLogLevel)); err != nil {
			lvl = slog.LevelInfo
		}
	}
	var slogHandler slog.Handler
	opts := &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				if level, ok := a.Value.Any().(slog.Level); ok {
					switch level {
					case logutil.LevelTrace:
						a.Value = slog.StringValue("TRACE")
					}
				}
			}
			return a
		},
	}
	switch flagLogFormat {
	case "json":
		slogHandler = slog.NewJSONHandler(os.Stderr, opts)
	default:
		slogHandler = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(slogHandler))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if flagRTPMin > 0 && flagRTPMax > 0 && flagRTPMax >= flagRTPMin {
		slog.Info("RTP port range", "min", flagRTPMin, "max", flagRTPMax)
	} else {
		slog.Info("RTP port range: OS-assigned")
	}

	server, err := sip.NewServer(flagAddr)
	if err != nil {
		slog.Error("Failed to create server", "error", err)
		os.Exit(1)
	}

	reg := sip.NewRegistrar()
	go reg.Start(ctx)

	server.SetFlowDeadCallback(reg.RemoveBindingsByFlowID)

	var dp dialplan.Dialplan
	if flagDialplan != "" {
		var err error
		dp, err = dialplan.NewFromFile(flagDialplan)
		if err != nil {
			slog.Error("Failed to load dialplan", "error", err)
			os.Exit(1)
		}
		slog.Info("Dialplan loaded", "path", flagDialplan)
	}

	h := b2bua.NewHandler(b2bua.Config{
		Registrar:      reg,
		SessionManager: media.NewSessionManager(),
		Server:         server,
		ServerIP:       serverIP,
		ServerAddr:     flagAddr,
		UACManager:     sip.NewUACManager(),
		Dialplan:       dp,
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

	slog.Info("SIP server listening", "addr", flagAddr, "transport", "UDP+TCP")

	<-ctx.Done()
	slog.Info("Shutting down...")
}
