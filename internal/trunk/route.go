package trunk

import "fmt"

func applyDigitManipulation(input string, stripDigits int, prefix string) string {
	result := input
	if stripDigits > 0 && len(result) >= stripDigits {
		result = result[stripDigits:]
	}
	if prefix != "" {
		result = prefix + result
	}
	return result
}

func (m *TrunkManager) MatchRoute(user string) (*Trunk, string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, r := range m.routes {
		locs := r.compiled.FindStringIndex(user)
		if locs == nil {
			continue
		}
		trunk, ok := m.trunks[r.TrunkName]
		if !ok {
			continue
		}
		transformed := applyDigitManipulation(user, r.StripDigits, r.Prefix)
		m.logger.Debug("route matched",
			"route", r.Name,
			"trunk", trunk.Name,
			"user", user,
			"transformed", transformed,
			"stripDigits", r.StripDigits,
			"prefix", r.Prefix)
		return trunk, transformed, true
	}
	return nil, "", false
}

func (m *TrunkManager) TrunkByName(name string) *Trunk {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.trunks[name]
}

func FormatTrunkContact(serverIP, serverAddr, transport string) string {
	return fmt.Sprintf("<sip:trec@%s;transport=%s>", serverIP, transport)
}
