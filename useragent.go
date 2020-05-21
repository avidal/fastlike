package fastlike

// UserAgent represents a user agent.
type UserAgent struct {
	Family string
	Major  string
	Minor  string
	Patch  string
}

type UserAgentParser func(uastring string) UserAgent

// UserAgUserAgentParserOption is an InstanceOption that converts user agent header values into
// UserAgent structs, called when the guest code uses the user agent parser XQD call.
func UserAgentParserOption(fn UserAgentParser) InstanceOption {
	return func(i *Instance) {
		i.uaparser = fn
	}
}
