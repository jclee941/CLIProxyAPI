package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

type flowLikenessResource struct {
	ID        string `json:"id"`
	Thumbnail string `json:"thumbnailBase64,omitempty"`
}

func (service *service) flowAccountRPC(ctx context.Context, record storageRecord, rpc string, args []any) (any, error) {
	var payload any
	err := service.withFlowSession(ctx, record, func(session *flowSession) error {
		var err error
		payload, err = session.rpc(ctx, rpc, args, flowProjectsPath, "")
		return err
	})
	return payload, err
}

func (service *service) flowLikenessHTTP(ctx context.Context, record storageRecord, tail string, request flowHTTPRequest) (httpResponse, error) {
	switch {
	case tail == "eligibility" && request.Method == http.MethodGet:
		payload, err := service.flowAccountRPC(ctx, record, "ve2Lsc", []any{})
		if err != nil {
			return httpResponse{}, err
		}
		flag := jsonField(payload, 0)
		if flag != nil && flag != true && flag != false && flag != float64(0) && flag != float64(1) {
			return httpResponse{}, failure(502, "flow_likeness_eligibility_invalid")
		}
		return flowJSON(200, map[string]bool{"eligible": flag == true || flag == float64(1)})
	case tail == "registrations" && request.Method == http.MethodPost:
		payload, err := service.flowAccountRPC(ctx, record, "T3Zezf", []any{})
		if err != nil {
			return httpResponse{}, err
		}
		link, _ := jsonField(payload, 0).(string)
		token, _ := jsonField(payload, 1).(string)
		parsed, err := url.Parse(link)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "myaccount.google.com" || parsed.User != nil || !flowUUIDPattern.MatchString(token) {
			return httpResponse{}, failure(502, "flow_likeness_registration_invalid")
		}
		return flowJSON(201, map[string]any{"url": link, "token": token, "requiresUserVerification": true})
	case strings.HasPrefix(tail, "registrations/") && request.Method == http.MethodGet:
		token := strings.TrimPrefix(tail, "registrations/")
		if !flowUUIDPattern.MatchString(token) {
			return httpResponse{}, failure(400, "flow_likeness_registration_token_invalid")
		}
		payload, err := service.flowAccountRPC(ctx, record, "lv2lXd", []any{token})
		if err != nil {
			return httpResponse{}, err
		}
		code, ok := jsonInteger(jsonField(payload, 0))
		if jsonField(payload, 0) == nil {
			code, ok = 0, true
		}
		if !ok {
			return httpResponse{}, failure(502, "flow_likeness_registration_status_invalid")
		}
		status := "unknown"
		switch code {
		case 1:
			status = "pending"
		case 2:
			status = "complete"
		case 3:
			status = "failed"
		}
		return flowJSON(200, map[string]any{"token": token, "status": status, "stateCode": code})
	}
	if request.Method != http.MethodGet && request.Method != http.MethodDelete || tail == "" && request.Method != http.MethodGet {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	payload, err := service.flowAccountRPC(ctx, record, "DTaVef", []any{1})
	if err != nil {
		return httpResponse{}, err
	}
	rows, _ := jsonField(payload, 0).([]any)
	resources := []flowLikenessResource{}
	for _, row := range rows {
		id, _ := jsonField(row, 0).(string)
		if !flowIdentifier(id) {
			return httpResponse{}, failure(502, "flow_likeness_response_invalid")
		}
		thumbnail, _ := jsonField(row, 3).(string)
		resources = append(resources, flowLikenessResource{ID: id, Thumbnail: thumbnail})
	}
	if tail == "" {
		return flowJSON(200, map[string]any{"likenesses": resources})
	}
	for _, resource := range resources {
		if resource.ID != tail {
			continue
		}
		if request.Method == http.MethodGet {
			return flowJSON(200, resource)
		}
		if _, err := service.flowAccountRPC(ctx, record, "KaLHHf", []any{resource.ID}); err != nil {
			return httpResponse{}, err
		}
		return httpResponse{StatusCode: 204, Headers: http.Header{"Cache-Control": {"private, no-store"}}}, nil
	}
	return httpResponse{}, failure(404, "flow_likeness_not_found")
}
