package fastlike

// XqdStatus is a status code returned from every XQD ABI method as described in crate
// `fastly-shared`
type XqdStatus int32

const (
	XqdStatusOK           XqdStatus = 0
	XqdError              XqdStatus = 1
	XqdErrInvalidArgument XqdStatus = 2
	XqdErrInvalidHandle   XqdStatus = 3
	XqdErrBufferLength    XqdStatus = 4
	XqdErrUnsupported     XqdStatus = 5
	XqdErrBadAlignment    XqdStatus = 6
	XqdErrHttpParse       XqdStatus = 7
	XqdErrHttpUserInvalid XqdStatus = 8
	XqdErrHttpIncomplete  XqdStatus = 9
)

type HttpVersion int32

const (
	Http09 HttpVersion = 0
	Http10 HttpVersion = 1
	Http11 HttpVersion = 2
	Http2  HttpVersion = 3
	Http3  HttpVersion = 4
)
