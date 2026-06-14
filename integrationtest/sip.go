package integrationtest

import (
	"strings"

	sipgo_sip "github.com/emiago/sipgo/sip"
)

// ExtractToTag extracts the tag parameter from a SIP To header.
func ExtractToTag(res *sipgo_sip.Response) string {
	to := res.GetHeader("To")
	if to == nil {
		return ""
	}
	val := to.Value()
	if idx := strings.Index(val, ";tag="); idx != -1 {
		return val[idx+5:]
	}
	return ""
}
