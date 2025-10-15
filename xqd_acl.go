package fastlike

import (
	"encoding/json"
	"net"
)

// xqd_acl_open opens an ACL by name and returns a handle to it.
// Returns HandleInvalid if the ACL doesn't exist.
func (i *Instance) xqd_acl_open(
	aclNamePtr int32,
	aclNameLen int32,
	aclHandleOut int32,
) int32 {
	// Read ACL name from guest memory
	buf := make([]byte, aclNameLen)
	_, err := i.memory.ReadAt(buf, int64(aclNamePtr))
	if err != nil {
		return XqdError
	}
	name := string(buf)

	i.abilog.Printf("acl_open: name=%s", name)

	// Look up the ACL by name
	acl := i.getACL(name)
	if acl == nil {
		i.abilog.Printf("acl_open: ACL not found: %s", name)
		return XqdErrNone // Return error when ACL doesn't exist (matches Viceroy)
	}

	// Create a handle for this ACL
	handle := i.aclHandles.New(name, acl)
	i.memory.PutUint32(uint32(handle), int64(aclHandleOut))

	i.abilog.Printf("acl_open: created handle=%d for ACL=%s", handle, name)

	return XqdStatusOK
}

// xqd_acl_lookup performs an ACL lookup for the given IP address.
// Returns a body handle containing the JSON-encoded ACL entry if found.
// The acl_error_out parameter indicates whether the lookup succeeded.
func (i *Instance) xqd_acl_lookup(
	aclHandle int32,
	ipOctetsPtr int32,
	ipLen int32,
	bodyHandleOut int32,
	aclErrorOut int32,
) int32 {
	// Get the ACL handle
	ah := i.aclHandles.Get(int(aclHandle))
	if ah == nil {
		i.abilog.Printf("acl_lookup: invalid ACL handle=%d", aclHandle)
		return XqdErrInvalidHandle
	}

	// Validate IP length (must be 4 for IPv4 or 16 for IPv6)
	if ipLen != 4 && ipLen != 16 {
		i.abilog.Printf("acl_lookup: invalid IP length=%d", ipLen)
		return XqdErrInvalidArgument
	}

	// Read IP octets from guest memory
	octets := make([]byte, ipLen)
	_, err := i.memory.ReadAt(octets, int64(ipOctetsPtr))
	if err != nil {
		i.abilog.Printf("acl_lookup: failed to read IP octets: %v", err)
		return XqdError
	}

	// Parse IP address
	var ip net.IP
	if ipLen == 4 {
		// IPv4
		ip = net.IPv4(octets[0], octets[1], octets[2], octets[3])
		i.abilog.Printf("acl_lookup: IPv4=%s in ACL=%s", ip.String(), ah.name)
	} else {
		// IPv6
		ip = net.IP(octets)
		i.abilog.Printf("acl_lookup: IPv6=%s in ACL=%s", ip.String(), ah.name)
	}

	// Perform the lookup
	entry := ah.acl.Lookup(ip)
	if entry == nil {
		// No match found
		i.abilog.Printf("acl_lookup: no match found for IP=%s", ip.String())
		i.memory.PutUint32(AclErrorNoContent, int64(aclErrorOut))
		return XqdStatusOK
	}

	// Match found - serialize the entry to JSON
	jsonBytes, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		i.abilog.Printf("acl_lookup: failed to marshal entry to JSON: %v", err)
		i.memory.PutUint32(AclErrorUninitialized, int64(aclErrorOut))
		return XqdError
	}

	i.abilog.Printf("acl_lookup: match found for IP=%s, entry=%s", ip.String(), string(jsonBytes))

	// Create a body handle with the JSON data
	bodyHandle, body := i.bodies.NewBuffer()
	_, _ = body.Write(jsonBytes)
	body.length = int64(len(jsonBytes))

	// Write the output parameters
	i.memory.PutUint32(uint32(bodyHandle), int64(bodyHandleOut))
	i.memory.PutUint32(AclErrorOk, int64(aclErrorOut))

	return XqdStatusOK
}
