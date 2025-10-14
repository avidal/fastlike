package fastlike

import "net/http"

// ImageOptimizerTransformConfig represents the configuration for image transformation
type ImageOptimizerTransformConfig struct {
	// SdkClaimsOpts contains any Image Optimizer API parameters that were set
	// as well as the Image Optimizer region the request is meant for.
	// This is typically a JSON string with transformation parameters like:
	// {"resize": {"width": 200, "height": 200}, "format": "webp", "quality": 80}
	SdkClaimsOpts string
}

// ImageOptimizerErrorDetail represents error information from image transformation
type ImageOptimizerErrorDetail struct {
	Tag     uint32 // One of ImageOptimizerErrorTag* constants
	Message string // Error or warning message
}

// ImageOptimizerTransformFunc is a function that transforms images according to
// the provided configuration. It receives:
// - originRequest: the HTTP request for the original image
// - originBody: the body of the original image (may be nil if not provided)
// - backend: the backend name to fetch the image from
// - config: transformation configuration (SDK claims, region, etc.)
//
// It returns:
// - response: the HTTP response containing the transformed image
// - errorDetail: error details if transformation failed
// - err: Go error if something went wrong at the infrastructure level
//
// If the function returns a non-nil error, XqdError will be returned to the guest.
// If errorDetail.Tag is ImageOptimizerErrorTagError or ImageOptimizerErrorTagWarning,
// the message will be passed back to the guest.
type ImageOptimizerTransformFunc func(
	originRequest *http.Request,
	originBody []byte,
	backend string,
	config ImageOptimizerTransformConfig,
) (*http.Response, ImageOptimizerErrorDetail, error)

// defaultImageOptimizer returns an unsupported error, as image transformation
// requires external libraries and is not implemented by default.
func defaultImageOptimizer(
	originRequest *http.Request,
	originBody []byte,
	backend string,
	config ImageOptimizerTransformConfig,
) (*http.Response, ImageOptimizerErrorDetail, error) {
	return nil, ImageOptimizerErrorDetail{
		Tag:     ImageOptimizerErrorTagError,
		Message: "Image Optimizer is not configured. Use WithImageOptimizer() to provide an image transformation function.",
	}, nil
}
