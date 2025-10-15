package fastlike

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Acl represents an Access Control List with entries for IP address matching.
type Acl struct {
	Entries []AclEntry `json:"entries"`
}

// AclEntry represents a single entry in an ACL with an IP prefix and action.
type AclEntry struct {
	Prefix string `json:"prefix"` // CIDR notation, e.g., "192.168.0.0/16"
	Action string `json:"action"` // "ALLOW" or "BLOCK"
	// The "op" field from Fastly's JSON format is intentionally ignored
	ip   net.IP // Normalized IP address
	mask uint8  // Network mask bits
}

// UnmarshalJSON implements custom JSON unmarshaling for AclEntry.
// It parses the prefix string and normalizes the IP based on the mask.
func (e *AclEntry) UnmarshalJSON(data []byte) error {
	// First unmarshal into a temporary struct to get the raw fields
	var raw struct {
		Op     string `json:"op"`     // Ignored, for compatibility with Fastly API format
		Prefix string `json:"prefix"` // Required
		Action string `json:"action"` // Required
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	e.Prefix = raw.Prefix
	e.Action = strings.ToUpper(raw.Action)

	// Parse the prefix (IP/MASK format)
	parts := strings.Split(raw.Prefix, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid prefix format '%s': want IP/MASK", raw.Prefix)
	}

	ip := net.ParseIP(parts[0])
	if ip == nil {
		return fmt.Errorf("invalid IP address in prefix: %s", parts[0])
	}

	mask, err := strconv.ParseUint(parts[1], 10, 8)
	if err != nil {
		return fmt.Errorf("invalid mask in prefix: %s", parts[1])
	}

	// Determine if IPv4 or IPv6 and validate mask range
	isIPv6 := strings.Contains(parts[0], ":")
	if isIPv6 {
		if mask < 1 || mask > 128 {
			return fmt.Errorf("mask outside allowed range [1, 128]: %d", mask)
		}
	} else {
		if mask < 1 || mask > 32 {
			return fmt.Errorf("mask outside allowed range [1, 32]: %d", mask)
		}
		// Convert IPv4 to 4-byte representation
		ip = ip.To4()
	}

	e.mask = uint8(mask)
	e.ip = normalizeIP(ip, e.mask)

	return nil
}

// MarshalJSON implements custom JSON marshaling for AclEntry.
func (e *AclEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Prefix string `json:"prefix"`
		Action string `json:"action"`
	}{
		Prefix: e.Prefix,
		Action: e.Action,
	})
}

// normalizeIP applies the network mask to an IP address to get the network prefix.
// This ensures that 192.168.100.200/16 becomes 192.168.0.0/16.
func normalizeIP(ip net.IP, mask uint8) net.IP {
	if ip == nil {
		return nil
	}

	ipLen := len(ip)
	switch ipLen {
	case net.IPv4len:
		// IPv4 - 4 bytes
		ipBits := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
		maskBits := uint32(0xFFFFFFFF) << (32 - mask)
		normalized := ipBits & maskBits
		// Return as 4-byte slice
		return net.IP{byte(normalized >> 24), byte(normalized >> 16), byte(normalized >> 8), byte(normalized)}
	case net.IPv6len:
		// IPv6 - 16 bytes
		var normalized [16]byte
		bits := mask
		for i := 0; i < 16; i++ {
			if bits >= 8 {
				normalized[i] = ip[i]
				bits -= 8
			} else if bits > 0 {
				maskByte := byte(0xFF) << (8 - bits)
				normalized[i] = ip[i] & maskByte
				bits = 0
			} else {
				normalized[i] = 0
			}
		}
		return net.IP(normalized[:])
	}

	return ip
}

// isMatch checks if the given IP matches this ACL entry's prefix and returns the mask length.
// Returns 0 if there is no match.
func (e *AclEntry) isMatch(ip net.IP) uint8 {
	if ip == nil || e.ip == nil {
		return 0
	}

	// Normalize both IPs to the same format
	entryIsV4 := len(e.ip) == net.IPv4len
	ipV4 := ip.To4()
	ipIsV4 := ipV4 != nil

	if entryIsV4 != ipIsV4 {
		return 0 // IPv4 and IPv6 don't match
	}

	// Convert input IP to same format as entry
	var compareIP net.IP
	if ipIsV4 {
		compareIP = ipV4 // Use 4-byte representation
	} else {
		compareIP = ip.To16() // Use 16-byte representation
	}

	// Normalize the input IP with the entry's mask
	normalized := normalizeIP(compareIP, e.mask)

	// Compare normalized IPs
	if !normalized.Equal(e.ip) {
		return 0
	}

	return e.mask
}

// Lookup performs an IP lookup in the ACL.
// If the IP matches multiple ACL entries, the most specific match is returned
// (longest mask), and in case of a tie, the last entry wins.
// Returns nil if no match is found.
func (a *Acl) Lookup(ip net.IP) *AclEntry {
	var bestMatch *AclEntry

	// Convert IPv4 to 4-byte representation for consistent matching
	if ip.To4() != nil {
		ip = ip.To4()
	}

	for i := range a.Entries {
		entry := &a.Entries[i]
		if matchMask := entry.isMatch(ip); matchMask > 0 {
			if bestMatch == nil || matchMask >= bestMatch.mask {
				bestMatch = entry
			}
		}
	}

	return bestMatch
}

// ParseACL parses an ACL from JSON data.
func ParseACL(data []byte) (*Acl, error) {
	var acl Acl
	if err := json.Unmarshal(data, &acl); err != nil {
		return nil, err
	}
	return &acl, nil
}

// addACL registers a new ACL with the given name.
func (i *Instance) addACL(name string, acl *Acl) {
	if i.acls == nil {
		i.acls = make(map[string]*Acl)
	}
	i.acls[name] = acl
}

// getACL retrieves an ACL by name. Returns nil if not found.
func (i *Instance) getACL(name string) *Acl {
	if i.acls == nil {
		return nil
	}
	return i.acls[name]
}
