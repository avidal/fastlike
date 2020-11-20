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

const (
	Http09 int32 = 0
	Http10 int32 = 1
	Http11 int32 = 2
	Http2  int32 = 3
	Http3  int32 = 4
)
