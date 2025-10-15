package fastlike

import (
	"encoding/json"
	"net"
)

func (i *Instance) xqd_geo_lookup(addr_octets int32, addr_len int32, buf int32, buf_len int32, nwritten_out int32) int32 {
	// Read the IP address octets
	octets := make([]byte, addr_len)
	_, err := i.memory.ReadAt(octets, int64(addr_octets))
	if err != nil {
		return XqdError
	}

	// Parse the IP address based on length
	var ip net.IP
	switch addr_len {
	case 4:
		// IPv4
		ip = net.IPv4(octets[0], octets[1], octets[2], octets[3])
	case 16:
		// IPv6
		ip = net.IP(octets)
	default:
		return XqdErrInvalidArgument
	}

	i.abilog.Printf("geo_lookup: ip=%s", ip.String())

	// Do the geolocation lookup
	geo := i.geolookup(ip)

	// Serialize to JSON
	result, err := json.Marshal(geo)
	if err != nil {
		return XqdError
	}

	// Check if buffer is large enough
	if len(result) > int(buf_len) {
		// Buffer too small - write the required size
		i.memory.PutUint32(uint32(len(result)), int64(nwritten_out))
		return XqdErrBufferLength
	}

	// Write the result to the buffer
	nwritten, err := i.memory.WriteAt(result, int64(buf))
	if err != nil {
		return XqdError
	}

	// Write the number of bytes written
	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	return XqdStatusOK
}
