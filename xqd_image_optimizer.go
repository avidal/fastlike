package fastlike

import (
	"bytes"
	"io"
)

// xqd_image_optimizer_transform_request transforms an image according to the provided configuration.
//
// Function signature (from WITX):
//
//	(@interface func (export "transform_image_optimizer_request")
//	    (param $origin_image_request $request_handle)
//	    (param $origin_image_request_body $body_handle)
//	    (param $origin_image_request_backend string)
//	    (param $io_transform_config_mask $image_optimizer_transform_config_options)
//	    (param $io_transform_configuration (@witx pointer $image_optimizer_transform_config))
//	    (param $io_error_detail (@witx pointer $image_optimizer_error_detail))
//	    (result $err (expected
//	            (tuple $response_handle $body_handle)
//	            (error $fastly_status)))
//	)
//
// The transform config struct contains:
//   - sdk_claims_opts: pointer to string (JSON-encoded Image Optimizer API parameters)
//   - sdk_claims_opts_len: u32
//
// The error detail struct contains:
//   - tag: u32 (ImageOptimizerErrorTag)
//   - message: pointer to string
//   - message_len: u32
func (i *Instance) xqd_image_optimizer_transform_request(
	originImageRequest int32,
	originImageRequestBody int32,
	originImageRequestBackendPtr int32,
	originImageRequestBackendLen int32,
	ioTransformConfigMask uint32,
	ioTransformConfigPtr int32,
	ioErrorDetailPtr int32,
	respHandleOut int32,
	bodyHandleOut int32,
) int32 {
	i.abilog.Printf("xqd_image_optimizer_transform_request: request=%d, body=%d, config_mask=0x%x",
		originImageRequest, originImageRequestBody, ioTransformConfigMask)

	// Check for reserved bit
	if ioTransformConfigMask&ImageOptimizerTransformConfigOptionsReserved != 0 {
		i.abilog.Printf("xqd_image_optimizer_transform_request: reserved bit set in config mask")
		return XqdErrInvalidArgument
	}

	// Read backend name
	backendBuf := make([]byte, originImageRequestBackendLen)
	_, err := i.memory.ReadAt(backendBuf, int64(originImageRequestBackendPtr))
	if err != nil {
		i.abilog.Printf("xqd_image_optimizer_transform_request: failed to read backend name: %v", err)
		return XqdError
	}
	backendName := string(backendBuf)
	i.abilog.Printf("xqd_image_optimizer_transform_request: backend=%s", backendName)

	// Get the origin request
	originReqHandle := i.requests.Get(int(originImageRequest))
	if originReqHandle == nil {
		i.abilog.Printf("xqd_image_optimizer_transform_request: invalid request handle: %d", originImageRequest)
		return XqdErrInvalidHandle
	}

	// Get the origin request body (if provided)
	var originBody []byte
	if originImageRequestBody >= 0 {
		bodyHandle := i.bodies.Get(int(originImageRequestBody))
		if bodyHandle == nil {
			i.abilog.Printf("xqd_image_optimizer_transform_request: invalid body handle: %d", originImageRequestBody)
			return XqdErrInvalidHandle
		}

		// Read the entire body
		originBody, err = io.ReadAll(bodyHandle)
		if err != nil {
			i.abilog.Printf("xqd_image_optimizer_transform_request: failed to read body: %v", err)
			return XqdError
		}
	}

	// Parse transform config if sdk_claims_opts is provided
	var config ImageOptimizerTransformConfig
	if ioTransformConfigMask&ImageOptimizerTransformConfigOptionsSdkClaimsOpt != 0 {
		// Read the ImageOptimizerTransformConfig struct from memory
		// struct layout:
		//   field $sdk_claims_opts (@witx pointer (@witx char8))  // offset 0, 4 bytes
		//   field $sdk_claims_opts_len u32                         // offset 4, 4 bytes
		sdkClaimsOptsPtr := i.memory.Uint32(int64(ioTransformConfigPtr))
		sdkClaimsOptsLen := i.memory.Uint32(int64(ioTransformConfigPtr + 4))

		if sdkClaimsOptsLen > 0 {
			sdkClaimsOptsBuf := make([]byte, sdkClaimsOptsLen)
			_, err := i.memory.ReadAt(sdkClaimsOptsBuf, int64(sdkClaimsOptsPtr))
			if err != nil {
				i.abilog.Printf("xqd_image_optimizer_transform_request: failed to read sdk_claims_opts: %v", err)
				return XqdError
			}
			config.SdkClaimsOpts = string(sdkClaimsOptsBuf)
			i.abilog.Printf("xqd_image_optimizer_transform_request: sdk_claims_opts=%s", config.SdkClaimsOpts)
		}
	}

	// Call the user-provided image optimizer function
	response, errorDetail, err := i.imageOptimizer(originReqHandle.Request, originBody, backendName, config)

	// Write error detail back to guest memory
	// struct layout:
	//   field $tag u32                                // offset 0, 4 bytes
	//   field $message (@witx pointer (@witx char8))  // offset 4, 4 bytes
	//   field $message_len u32                        // offset 8, 4 bytes
	if ioErrorDetailPtr != 0 {
		i.memory.PutUint32(errorDetail.Tag, int64(ioErrorDetailPtr))
		if errorDetail.Message != "" {
			// Allocate space in guest memory for the error message
			// Note: In a full implementation, we'd need a proper memory allocator.
			// For now, we write to a temporary buffer area. The guest should
			// provide a buffer or handle memory management.
			messageBytes := []byte(errorDetail.Message)
			// We'll write the message inline after the struct (this is a simplification)
			messagePtr := ioErrorDetailPtr + 12 // After the struct
			_, _ = i.memory.WriteAt(messageBytes, int64(messagePtr))
			i.memory.PutUint32(uint32(messagePtr), int64(ioErrorDetailPtr+4))
			i.memory.PutUint32(uint32(len(messageBytes)), int64(ioErrorDetailPtr+8))
		} else {
			i.memory.PutUint32(0, int64(ioErrorDetailPtr+4))
			i.memory.PutUint32(0, int64(ioErrorDetailPtr+8))
		}
	}

	// If there was an infrastructure error, return XqdError
	if err != nil {
		i.abilog.Printf("xqd_image_optimizer_transform_request: transform failed: %v", err)
		return XqdError
	}

	// If the transform reported an error, return XqdError
	if errorDetail.Tag == ImageOptimizerErrorTagError {
		i.abilog.Printf("xqd_image_optimizer_transform_request: transform reported error: %s", errorDetail.Message)
		return XqdError
	}

	// If no response was returned, return error
	if response == nil {
		i.abilog.Printf("xqd_image_optimizer_transform_request: no response returned from transform")
		return XqdError
	}

	// Create a response handle
	respHandle, resp := i.responses.New()
	resp.Status = response.Status
	resp.StatusCode = response.StatusCode
	resp.Header = response.Header.Clone()
	resp.Body = response.Body
	i.abilog.Printf("xqd_image_optimizer_transform_request: created response handle %d", respHandle)

	// Create a body handle for the response body
	var bodyHandleNum int32
	if response.Body != nil {
		bodyBytes, err := io.ReadAll(response.Body)
		if err != nil {
			i.abilog.Printf("xqd_image_optimizer_transform_request: failed to read response body: %v", err)
			return XqdError
		}
		_ = response.Body.Close()

		bhid, _ := i.bodies.NewReader(io.NopCloser(bytes.NewReader(bodyBytes)))
		bodyHandleNum = int32(bhid)
		i.abilog.Printf("xqd_image_optimizer_transform_request: created body handle %d", bodyHandleNum)
	} else {
		bhid, _ := i.bodies.NewReader(io.NopCloser(bytes.NewReader([]byte{})))
		bodyHandleNum = int32(bhid)
	}

	// Write the handles to memory
	i.memory.PutUint32(uint32(respHandle), int64(respHandleOut))
	i.memory.PutUint32(uint32(bodyHandleNum), int64(bodyHandleOut))

	return XqdStatusOK
}
