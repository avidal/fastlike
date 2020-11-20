package fastlike

// UserAgent represents a user agent.
type UserAgent struct {
	Family string
	Major  string
	Minor  string
	Patch  string
}

type UserAgentParser func(uastring string) UserAgent
