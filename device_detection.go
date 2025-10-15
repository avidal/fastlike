package fastlike

// DeviceLookupFunc is a function that extracts device detection data from a user agent string.
// Returns device information as a JSON string, or an empty string if no data is available.
// The JSON typically includes fields like device type, OS, browser, etc.
type DeviceLookupFunc func(userAgent string) string

// defaultDeviceDetection returns an empty string for all user agents,
// indicating no device detection data is available.
func defaultDeviceDetection(userAgent string) string {
	return ""
}
