package main

import "fmt"

// webRPCError identifies a definitive upstream rejection, rather than a
// transport failure whose submission outcome may still be unknown.
type webRPCError struct {
	*publicError
}

func (err *webRPCError) Unwrap() error { return err.publicError }

// webFrameError follows the web client's envelope: a string payload is data;
// otherwise wrb.fr field 5 carries google.rpc.Status. Executor errors carry the
// same status code in field 9. Messages and details may contain private input,
// so only the numeric code is exposed.
func webFrameError(frame any) error {
	var status any
	switch jsonField(frame, 0) {
	case "wrb.fr":
		if _, data := jsonField(frame, 2).(string); data || jsonField(frame, 5) == nil {
			return nil
		}
		status = jsonField(frame, 5, 0)
	case "er":
		status = jsonField(frame, 9)
	default:
		return nil
	}
	rpcCode, _ := jsonInteger(status)
	httpStatus, code := 502, "web_upstream_error"
	switch rpcCode {
	case 1:
		code = "web_upstream_cancelled"
	case 3, 11:
		httpStatus, code = 400, "web_request_invalid"
	case 4:
		httpStatus, code = 504, "web_upstream_timeout"
	case 5:
		httpStatus, code = 404, "web_upstream_not_found"
	case 6, 10:
		httpStatus, code = 409, "web_upstream_conflict"
	case 7:
		httpStatus, code = 403, "web_rpc_denied"
	case 8:
		httpStatus, code = 429, "web_quota_exhausted"
	case 9:
		httpStatus, code = 400, "web_failed_precondition"
	case 12:
		httpStatus, code = 501, "web_upstream_unsupported"
	case 13:
		code = "web_upstream_internal"
	case 14:
		httpStatus, code = 503, "web_upstream_unavailable"
	case 15:
		code = "web_response_invalid"
	case 16:
		return &AuthenticationFailure{}
	}
	return &webRPCError{&publicError{
		Code: code, Message: fmt.Sprintf("%s: rpc_code=%d", code, rpcCode), HTTPStatus: httpStatus,
	}}
}
