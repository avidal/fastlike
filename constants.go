package fastlike

// Constants used for return values from ABI functions.
// See https://docs.rs/fastly-shared for more.
const (
	XqdStatusOK           int32 = 0
	XqdError              int32 = 1
	XqdErrInvalidArgument int32 = 2
	XqdErrInvalidHandle   int32 = 3
	XqdErrBufferLength    int32 = 4
	XqdErrUnsupported     int32 = 5
	XqdErrBadAlignment    int32 = 6
	XqdErrHttpParse       int32 = 7
	XqdErrHttpUserInvalid int32 = 8
	XqdErrHttpIncomplete  int32 = 9
	XqdErrNone            int32 = 10
	XqdErrAgain           int32 = 11
	XqdErrLimitExceeded   int32 = 12
)

// HandleInvalid is returned to guests when they attempt to obtain a handle that doesn't exist. For
// instance, opening a dictionary that is not registered
// Note that this is dictinct from XqdErrInvalidHandle, which is returned when callers attempt to
// *use* an otherwise invalid handle, such as attempting to send a request whose handle has not
// been created.
const HandleInvalid = 4294967295 - 1

const (
	Http09 int32 = 0
	Http10 int32 = 1
	Http11 int32 = 2
	Http2  int32 = 3
	Http3  int32 = 4
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
