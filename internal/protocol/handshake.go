package protocol

import "fmt"

type ConnectRequest struct {
	MCPL   int    `json:"mcpl"`
	Type   string `json:"type"`
	Server string `json:"server"`
}

type ConnectResponse struct {
	MCPL    int    `json:"mcpl"`
	Type    string `json:"type"`
	Status  string `json:"status,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func NewConnectedResponse(status string) *ConnectResponse {
	return &ConnectResponse{
		MCPL:   1,
		Type:   "connected",
		Status: status,
	}
}

func NewErrorResponse(code, message string) *ConnectResponse {
	return &ConnectResponse{
		MCPL:    1,
		Type:    "error",
		Code:    code,
		Message: message,
	}
}

func ValidateHandshake(req *ConnectRequest, protocolVersion int) error {
	if req.MCPL != protocolVersion {
		return fmt.Errorf(
			"protocol version mismatch: daemon is v%d, client is v%d. Run `mcpl stop` and retry",
			protocolVersion, req.MCPL,
		)
	}
	if req.Type != "connect" {
		return fmt.Errorf("invalid handshake type: %q (expected \"connect\")", req.Type)
	}
	if req.Server == "" {
		return fmt.Errorf("server name is required in connect request")
	}
	return nil
}
