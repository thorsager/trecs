package sip

import (
	"context"
	"testing"

	"github.com/thorsager/trecs/internal/logutil"
	"github.com/thorsager/trecs/proto"
)

func BenchmarkNISTRespond(b *testing.B) {
	tm := NewTransactionManager()
	trans := &mockTransport{}
	req := testRequest(b, proto.SIPMethodOPTIONS, "bench-nist", true)
	res200 := proto.NewResponse(req, 200, "OK")

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		b.StopTimer()
		tx := &NonInviteTransaction{
			branch:    "bench-nist",
			method:    proto.SIPMethodOPTIONS,
			transport: trans,
			manager:   tm,
			reliable:  true,
			logger:    logutil.NewTestLogger(b),
		}
		b.StartTimer()

		tx.Respond(res200)
	}
}

func BenchmarkNISTRespondProvisionalThenFinal(b *testing.B) {
	tm := NewTransactionManager()
	trans := &mockTransport{}
	req := testRequest(b, proto.SIPMethodOPTIONS, "bench-nist-seq", true)
	res100 := proto.NewResponse(req, 100, "Trying")
	res200 := proto.NewResponse(req, 200, "OK")

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		b.StopTimer()
		tx := &NonInviteTransaction{
			branch:    "bench-nist-seq",
			method:    proto.SIPMethodOPTIONS,
			state:     NISTTrying,
			transport: trans,
			manager:   tm,
			reliable:  true,
			logger:    logutil.NewTestLogger(b),
		}
		b.StartTimer()

		tx.Respond(res100)
		tx.Respond(res200)
	}
}

func BenchmarkISTRespond2xx(b *testing.B) {
	tm := NewTransactionManager()
	trans := &mockTransport{}
	req := testRequest(b, proto.SIPMethodINVITE, "bench-ist-2xx", true)
	res200 := proto.NewResponse(req, 200, "OK")

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		b.StopTimer()
		tx := &InviteTransaction{
			branch:    "bench-ist-2xx",
			state:     ISTTrying,
			transport: trans,
			manager:   tm,
			reliable:  true,
			logger:    logutil.NewTestLogger(b),
		}
		b.StartTimer()

		tx.Respond(res200)
	}
}

func BenchmarkISTRespond300Plus(b *testing.B) {
	tm := NewTransactionManager()
	trans := &mockTransport{}
	req := testRequest(b, proto.SIPMethodINVITE, "bench-ist-300", true)
	res404 := proto.NewResponse(req, 404, "Not Found")

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		b.StopTimer()
		tx := &InviteTransaction{
			branch:    "bench-ist-300",
			state:     ISTTrying,
			transport: trans,
			manager:   tm,
			reliable:  true,
			logger:    logutil.NewTestLogger(b),
		}
		b.StartTimer()

		tx.Respond(res404)
	}
}

func BenchmarkISTRespondProvisionalThen300(b *testing.B) {
	tm := NewTransactionManager()
	trans := &mockTransport{}
	req := testRequest(b, proto.SIPMethodINVITE, "bench-ist-seq", true)
	res180 := proto.NewResponse(req, 180, "Ringing")
	res404 := proto.NewResponse(req, 404, "Not Found")

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		b.StopTimer()
		tx := &InviteTransaction{
			branch:    "bench-ist-seq",
			state:     ISTTrying,
			transport: trans,
			manager:   tm,
			reliable:  true,
			logger:    logutil.NewTestLogger(b),
		}
		b.StartTimer()

		tx.Respond(res180)
		tx.Respond(res404)
	}
}

func BenchmarkManagerHandleRequestNew(b *testing.B) {
	trans := &mockTransport{}
	req := testRequest(b, proto.SIPMethodOPTIONS, "bench-mgr-new", true)
	ev := MessageEvent{Msg: req, Target: Target{}}

	handler := func(ctx context.Context, r *proto.SIPMessage, tx Transaction) {
		tx.Respond(proto.NewResponse(r, 200, "OK"))
	}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		b.StopTimer()
		tm := NewTransactionManager()
		b.StartTimer()

		tm.HandleRequest(b.Context(), ev, trans, handler)
	}
}

func BenchmarkManagerHandleRequestRetransmission(b *testing.B) {
	trans := &mockTransport{}
	req := testRequest(b, proto.SIPMethodOPTIONS, "bench-mgr-retrans", true)
	ev := MessageEvent{Msg: req, Target: Target{}}

	tm := NewTransactionManager()
	tm.HandleRequest(b.Context(), ev, trans, func(ctx context.Context, r *proto.SIPMessage, tx Transaction) {
		tx.Respond(proto.NewResponse(r, 200, "OK"))
	})

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		tm.HandleRequest(b.Context(), ev, trans, nil)
	}
}

func BenchmarkManagerHandleACK(b *testing.B) {
	trans := &mockTransport{}
	req := testRequest(b, proto.SIPMethodINVITE, "bench-ack", true)
	ev := MessageEvent{Msg: req, Target: Target{}}

	tm := NewTransactionManager()
	tm.HandleRequest(b.Context(), ev, trans, func(ctx context.Context, r *proto.SIPMessage, tx Transaction) {
		tx.Respond(proto.NewResponse(r, 404, "Not Found"))
	})

	ackRaw := "ACK sip:test SIP/2.0\r\n" +
		"Via: SIP/2.0/TCP 127.0.0.1;branch=bench-ack\r\n" +
		"From: <sip:a>;tag=tag1\r\n" +
		"To: <sip:b>;tag=tag2\r\n" +
		"Call-ID: test-call\r\n" +
		"CSeq: 1 ACK\r\n" +
		"Content-Length: 0\r\n\r\n"
	ack, _ := proto.UnmarshalSIPDatagram([]byte(ackRaw))
	ackEv := MessageEvent{Msg: ack, Target: Target{}}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		tm.HandleACK(b.Context(), ackEv, trans)
	}
}

func BenchmarkManagerHandleACKNoMatch(b *testing.B) {
	trans := &mockTransport{}
	tm := NewTransactionManager()

	ackRaw := "ACK sip:test SIP/2.0\r\n" +
		"Via: SIP/2.0/TCP 127.0.0.1;branch=bench-ack-miss\r\n" +
		"From: <sip:a>;tag=1\r\n" +
		"To: <sip:b>\r\n" +
		"Call-ID: no-match\r\n" +
		"CSeq: 1 ACK\r\n" +
		"Content-Length: 0\r\n\r\n"
	ack, _ := proto.UnmarshalSIPDatagram([]byte(ackRaw))
	ackEv := MessageEvent{Msg: ack, Target: Target{}}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		tm.HandleACK(b.Context(), ackEv, trans)
	}
}
