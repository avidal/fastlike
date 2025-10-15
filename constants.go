package fastlike

// XQD ABI status codes returned from host functions.
// These match the fastly-shared Rust crate definitions.
// See https://docs.rs/fastly-shared for more details.
const (
	XqdStatusOK           int32 = 0  // Success
	XqdError              int32 = 1  // Generic error
	XqdErrInvalidArgument int32 = 2  // Invalid argument passed
	XqdErrInvalidHandle   int32 = 3  // Invalid handle ID
	XqdErrBufferLength    int32 = 4  // Buffer too small
	XqdErrUnsupported     int32 = 5  // Operation not supported
	XqdErrBadAlignment    int32 = 6  // Misaligned pointer
	XqdErrHttpParse       int32 = 7  // HTTP parsing error
	XqdErrHttpUserInvalid int32 = 8  // Invalid HTTP user input
	XqdErrHttpIncomplete  int32 = 9  // Incomplete HTTP message
	XqdErrNone            int32 = 10 // No value/data available
	XqdErrAgain           int32 = 11 // Operation would block (try again)
	XqdErrLimitExceeded   int32 = 12 // Resource limit exceeded
)

// HandleInvalid is returned when attempting to open a resource that doesn't exist
// (e.g., opening a dictionary that was not registered).
//
// This is distinct from XqdErrInvalidHandle, which is returned when using an invalid
// handle ID (e.g., a handle that was never created or already closed).
//
// Value is uint32_max - 1 (4294967294).
const HandleInvalid = 4294967295 - 1

// HTTP version constants for request/response version fields.
const (
	Http09 int32 = 0 // HTTP/0.9
	Http10 int32 = 1 // HTTP/1.0
	Http11 int32 = 2 // HTTP/1.1
	Http2  int32 = 3 // HTTP/2
	Http3  int32 = 4 // HTTP/3
)

// SendErrorDetailTag represents different error types that can occur during send operations
const (
	SendErrorDetailUninitialized uint32 = iota
	SendErrorDetailOk
	SendErrorDetailDnsTimeout
	SendErrorDetailDnsError
	SendErrorDetailDestinationNotFound
	SendErrorDetailDestinationUnavailable
	SendErrorDetailDestinationIpUnroutable
	SendErrorDetailConnectionRefused
	SendErrorDetailConnectionTerminated
	SendErrorDetailConnectionTimeout
	SendErrorDetailConnectionLimitReached
	SendErrorDetailTlsCertificateError
	SendErrorDetailTlsConfigurationError
	SendErrorDetailHttpIncompleteResponse
	SendErrorDetailHttpResponseHeaderSectionTooLarge
	SendErrorDetailHttpResponseBodyTooLarge
	SendErrorDetailHttpResponseTimeout
	SendErrorDetailHttpResponseStatusInvalid
	SendErrorDetailHttpUpgradeFailed
	SendErrorDetailHttpProtocolError
	SendErrorDetailHttpRequestCacheKeyInvalid
	SendErrorDetailHttpRequestUriInvalid
	SendErrorDetailInternalError
	SendErrorDetailTlsAlertReceived
	SendErrorDetailTlsProtocolError
)

// SendErrorDetailMask represents which fields in the error detail are valid
const (
	SendErrorDetailMaskReserved      uint32 = 1 << 0
	SendErrorDetailMaskDnsErrorRcode uint32 = 1 << 1
	SendErrorDetailMaskDnsErrorInfo  uint32 = 1 << 2
	SendErrorDetailMaskTlsAlertId    uint32 = 1 << 3
)

// Backend health status constants
const (
	BackendHealthUnknown   uint32 = 0
	BackendHealthHealthy   uint32 = 1
	BackendHealthUnhealthy uint32 = 2
)

// Cache lookup state flags
const (
	CacheLookupStateFound              uint32 = 1 << 0
	CacheLookupStateUsable             uint32 = 1 << 1
	CacheLookupStateStale              uint32 = 1 << 2
	CacheLookupStateMustInsertOrUpdate uint32 = 1 << 3
)

// Cache lookup options mask
const (
	CacheLookupOptionsMaskReserved                uint32 = 1 << 0
	CacheLookupOptionsMaskRequestHeaders          uint32 = 1 << 1
	CacheLookupOptionsMaskService                 uint32 = 1 << 2
	CacheLookupOptionsMaskAlwaysUseRequestedRange uint32 = 1 << 3
)

// Cache write options mask
const (
	CacheWriteOptionsMaskReserved               uint32 = 1 << 0
	CacheWriteOptionsMaskRequestHeaders         uint32 = 1 << 1
	CacheWriteOptionsMaskVaryRule               uint32 = 1 << 2
	CacheWriteOptionsMaskInitialAgeNs           uint32 = 1 << 3
	CacheWriteOptionsMaskStaleWhileRevalidateNs uint32 = 1 << 4
	CacheWriteOptionsMaskSurrogateKeys          uint32 = 1 << 5
	CacheWriteOptionsMaskLength                 uint32 = 1 << 6
	CacheWriteOptionsMaskUserMetadata           uint32 = 1 << 7
	CacheWriteOptionsMaskSensitiveData          uint32 = 1 << 8
	CacheWriteOptionsMaskEdgeMaxAgeNs           uint32 = 1 << 9
	CacheWriteOptionsMaskService                uint32 = 1 << 10
)

// Cache get body options mask
const (
	CacheGetBodyOptionsMaskReserved uint32 = 1 << 0
	CacheGetBodyOptionsMaskFrom     uint32 = 1 << 1
	CacheGetBodyOptionsMaskTo       uint32 = 1 << 2
)

// Cache replace options mask
const (
	CacheReplaceOptionsMaskReserved                uint32 = 1 << 0
	CacheReplaceOptionsMaskRequestHeaders          uint32 = 1 << 1
	CacheReplaceOptionsMaskReplaceStrategy         uint32 = 1 << 2
	CacheReplaceOptionsMaskService                 uint32 = 1 << 3
	CacheReplaceOptionsMaskAlwaysUseRequestedRange uint32 = 1 << 4
)

// ContentEncodings flags for auto-decompression
const (
	ContentEncodingsGzip uint32 = 1 << 0
)

// BodyWriteEnd specifies where to write data in a body.
const (
	BodyWriteEndBack  int32 = 0 // Append to the end of the body
	BodyWriteEndFront int32 = 1 // Prepend to the beginning of the body
)

// AclError represents ACL lookup errors
const (
	AclErrorUninitialized   uint32 = 0
	AclErrorOk              uint32 = 1
	AclErrorNoContent       uint32 = 2
	AclErrorTooManyRequests uint32 = 3
)

// PurgeOptions mask flags
const (
	PurgeOptionsSoftPurge uint32 = 1 << 0 // Perform soft purge instead of hard purge
	PurgeOptionsRetBuf    uint32 = 1 << 1 // Return JSON purge response in buffer
)

// BackendConfigOptions represents mask flags for dynamic backend configuration
const (
	BackendConfigOptionsReserved            uint32 = 1 << 0
	BackendConfigOptionsHostOverride        uint32 = 1 << 1
	BackendConfigOptionsConnectTimeout      uint32 = 1 << 2
	BackendConfigOptionsFirstByteTimeout    uint32 = 1 << 3
	BackendConfigOptionsBetweenBytesTimeout uint32 = 1 << 4
	BackendConfigOptionsUseSSL              uint32 = 1 << 5
	BackendConfigOptionsSSLMinVersion       uint32 = 1 << 6
	BackendConfigOptionsSSLMaxVersion       uint32 = 1 << 7
	BackendConfigOptionsCertHostname        uint32 = 1 << 8
	BackendConfigOptionsCACert              uint32 = 1 << 9
	BackendConfigOptionsCiphers             uint32 = 1 << 10
	BackendConfigOptionsSNIHostname         uint32 = 1 << 11
	BackendConfigOptionsDontPool            uint32 = 1 << 12
	BackendConfigOptionsClientCert          uint32 = 1 << 13
	BackendConfigOptionsGRPC                uint32 = 1 << 14
	BackendConfigOptionsKeepalive           uint32 = 1 << 15
)

// TLS version constants
const (
	TLSv10 uint32 = 0
	TLSv11 uint32 = 1
	TLSv12 uint32 = 2
	TLSv13 uint32 = 3
)

// ClientCertVerifyResult represents the result of client certificate verification
const (
	ClientCertVerifyResultOk                 uint32 = 0
	ClientCertVerifyResultBadCertificate     uint32 = 1
	ClientCertVerifyResultCertificateRevoked uint32 = 2
	ClientCertVerifyResultCertificateExpired uint32 = 3
	ClientCertVerifyResultUnknownCA          uint32 = 4
	ClientCertVerifyResultCertificateMissing uint32 = 5
	ClientCertVerifyResultCertificateUnknown uint32 = 6
)

// ImageOptimizerTransformConfigOptions represents mask flags for image optimizer transform configuration
const (
	ImageOptimizerTransformConfigOptionsReserved     uint32 = 1 << 0
	ImageOptimizerTransformConfigOptionsSdkClaimsOpt uint32 = 1 << 1
)

// ImageOptimizerErrorTag represents the status of an image optimizer operation
const (
	ImageOptimizerErrorTagUninitialized uint32 = 0
	ImageOptimizerErrorTagOk            uint32 = 1
	ImageOptimizerErrorTagError         uint32 = 2
	ImageOptimizerErrorTagWarning       uint32 = 3
)

// KV store error codes (kv_error enum from compute-at-edge.witx)
const (
	KvErrorUninitialized      uint32 = 0
	KvErrorOk                 uint32 = 1
	KvErrorBadRequest         uint32 = 2
	KvErrorNotFound           uint32 = 3
	KvErrorPreconditionFailed uint32 = 4
	KvErrorPayloadTooLarge    uint32 = 5
	KvErrorInternalError      uint32 = 6
	KvErrorTooManyRequests    uint32 = 7
)
