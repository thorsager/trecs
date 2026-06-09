package b2bua

import (
	"testing"

	"github.com/thorsager/trecs/integrationtest"
)

func TestIntegration_B2BUA(t *testing.T) {
	t.Run("S1_BasicCall_UDP_BYEFromAlice", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", nil)
		defer ts.Stop()
		runB2BUACall(t, ts, "udp", "udp", "alice_bye")
	})

	t.Run("S2_BobHangsUp_UDP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", nil)
		defer ts.Stop()

		runB2BUACall(t, ts, "udp", "udp", "bob_bye")
	})

	t.Run("S3_BobRejects_UDP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", nil)
		defer ts.Stop()

		runB2BUAReject(t, ts, "udp", "udp")
	})

	t.Run("S4_BasicCall_TCP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", nil)
		defer ts.Stop()

		runB2BUACall(t, ts, "tcp", "tcp", "alice_bye")
	})

	t.Run("S5_AliceTCP_BobUDP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", nil)
		defer ts.Stop()

		runB2BUACall(t, ts, "tcp", "udp", "alice_bye")
	})

	t.Run("S6_AliceUDP_BobTCP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", nil)
		defer ts.Stop()

		runB2BUACall(t, ts, "udp", "tcp", "alice_bye")
	})

	t.Run("S7_OutboundTCP_Bob_UDP_Alice", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", nil)
		defer ts.Stop()
		runOutboundCall(t, ts, "bob")
	})

	t.Run("S8_OutboundTCP_Alice_UDP_Bob", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", nil)
		defer ts.Stop()
		runOutboundCall(t, ts, "alice")
	})
}
