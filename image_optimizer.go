package fastlike

import "net/http"

// ImageOptimizerTransformConfig represents the configuration for image transformation.
type ImageOptimizerTransformConfig struct {
	// SdkClaimsOpts contains Image Optimizer API parameters and target region.
	// This is a JSON string with transformation parameters, for example:
	//   {"resize": {"width": 200, "height": 200}, "format": "webp", "quality": 80}
	SdkClaimsOpts string
}

// ImageOptimizerErrorDetail represents error information from image transformation
type ImageOptimizerErrorDetail struct {
	Tag     uint32 // One of ImageOptimizerErrorTag* constants
	Message string // Error or warning message
}

// ImageOptimizerTransformFunc is a function that transforms images according to config.
//
// Inputs:
//   - originRequest: HTTP request for the original image
//   - originBody: body bytes of the original image (may be nil if not yet fetched)
//   - backend: backend name to fetch the image from (if originBody is nil)
//   - config: transformation configuration (resize, format, quality, etc.)
//
// Outputs:
//   - response: HTTP response containing the transformed image
//   - errorDetail: structured error/warning information
//   - err: Go error if infrastructure-level failure occurred
//
// Error handling:
//   - If err is non-nil, XqdError is returned to the guest
//   - If errorDetail.Tag indicates error/warning, the message is passed to the guest
type ImageOptimizerTransformFunc func(
	originRequest *http.Request,
	originBody []byte,
	backend string,
	config ImageOptimizerTransformConfig,
) (*http.Response, ImageOptimizerErrorDetail, error)

// defaultImageOptimizer returns an error indicating image optimization is not configured.
// Image transformation requires external libraries (e.g., libvips) and must be
// explicitly configured via WithImageOptimizer().
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
