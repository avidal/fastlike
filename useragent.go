package fastlike

// UserAgent represents a user agent.
type UserAgent struct {
	Family string
	Major  string
	Minor  string
	Patch  string
}

// UserAgentParser is a function that parses a user agent string and returns structured UserAgent data.
type UserAgentParser func(uastring string) UserAgent
