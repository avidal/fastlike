package fastlike

// DeviceLookupFunc is a function that takes a user agent string and returns
// device detection data as a JSON string. It returns an empty string if no
// device detection data is available for the given user agent.
type DeviceLookupFunc func(userAgent string) string

// defaultDeviceDetection returns an empty string for all user agents,
// indicating no device detection data is available.
func defaultDeviceDetection(userAgent string) string {
	return ""
}
