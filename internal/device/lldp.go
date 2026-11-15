package device

import (
	"regexp"
	"strings"
)

// LLDPNeighbor to sąsiad z `show lldp neighbors detail` (AlliedWare Plus).
type LLDPNeighbor struct {
	LocalPort         string `json:"localPort"`
	ChassisID         string `json:"chassisId,omitempty"`
	PortID            string `json:"portId,omitempty"`
	PortDescription   string `json:"portDescription,omitempty"`
	SystemName        string `json:"systemName,omitempty"`
	SystemDescription string `json:"systemDescription,omitempty"`
	Capabilities      string `json:"capabilities,omitempty"`
	ManagementAddress string `json:"managementAddress,omitempty"`
}

var (
	lldpLocalRE = regexp.MustCompile(`^Local (\S+?):?\s*$`)
	// "  System Name ...................... 6F-SH210-1"
	lldpFieldRE = regexp.MustCompile(`^\s{1,4}(\S[^.]*?)\s*(?:- (\S[^.]*?))?\s*\.{2,}\s?(.*)$`)
	// Linia kontynuacji: same spacje i tekst (np. druga linia System Description).
	lldpContRE = regexp.MustCompile(`^\s{20,}(\S.*)$`)
	// "                      - Enabled .... Bridge" (podpole poprzedniego pola)
	lldpSubRE = regexp.MustCompile(`^\s+- (\S[^.]*?)\s*\.{2,}\s?(.*)$`)
)

// ParseLLDPDetail rozpoznaje bloki "Local portX:" i ich pola. Wartości
// "[not advertised]" są pomijane; wielolinijkowe opisy są łączone.
func ParseLLDPDetail(raw string) []LLDPNeighbor {
	var out []LLDPNeighbor
	var cur *LLDPNeighbor
	last := ""
	flush := func() {
		if cur != nil && (cur.ChassisID != "" || cur.SystemName != "" || cur.PortID != "") {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r", ""), "\n") {
		if m := lldpLocalRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil && strings.HasPrefix(strings.TrimSpace(line), "Local ") {
			flush()
			cur = &LLDPNeighbor{LocalPort: m[1]}
			last = ""
			continue
		}
		if cur == nil {
			continue
		}
		if m := lldpFieldRE.FindStringSubmatch(line); m != nil {
			key, sub, val := strings.TrimSpace(m[1]), strings.TrimSpace(m[2]), strings.TrimSpace(m[3])
			if strings.EqualFold(val, "[not advertised]") {
				val = ""
			}
			last = ""
			switch key {
			case "Chassis ID":
				cur.ChassisID, last = val, "chassis"
			case "Port ID":
				cur.PortID, last = val, "port"
			case "Port Description":
				cur.PortDescription, last = val, "pdesc"
			case "System Name":
				cur.SystemName, last = val, "name"
			case "System Description":
				cur.SystemDescription, last = val, "sdesc"
			case "System Capabilities":
				if sub == "" || strings.EqualFold(sub, "Enabled") || cur.Capabilities == "" {
					cur.Capabilities = val
				}
				last = "caps"
			case "Management Addresses":
				cur.ManagementAddress, last = val, "mgmt"
			}
			continue
		}
		if m := lldpSubRE.FindStringSubmatch(line); m != nil {
			if last == "caps" && strings.EqualFold(strings.TrimSpace(m[1]), "Enabled") {
				cur.Capabilities = strings.TrimSpace(m[2])
			}
			continue
		}
		if m := lldpContRE.FindStringSubmatch(line); m != nil {
			switch last {
			case "sdesc":
				cur.SystemDescription = strings.TrimSpace(cur.SystemDescription + " " + m[1])
			case "pdesc":
				cur.PortDescription = strings.TrimSpace(cur.PortDescription + " " + m[1])
			}
		}
	}
	flush()
	return out
}
