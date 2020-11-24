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
