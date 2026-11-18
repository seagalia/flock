package bigip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"
)

type DeclareResult struct {
	// Changed is false when AS3 found the tenant already as declared.
	Changed bool
	Message string
}

// Declare posts an AS3 declaration of one tenant and waits for its task. The post is asynchronous: a synchronous
// one holds the connection for the whole deployment, which proxies in front of a BIG-IP often cut.
func (c *Client) Declare(ctx context.Context, tenant string, body []byte) (DeclareResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.RequestTimeout)
	defer cancel()
	device, err := c.device(ctx)
	if err != nil {
		return DeclareResult{}, err
	}
	resp, err := c.do(ctx, device, http.MethodPost, "/mgmt/shared/appsvcs/declare?async=true", body)
	if err != nil {
		return DeclareResult{}, err
	}
	var accepted struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(resp.body, &accepted)
	if accepted.ID == "" {
		return tenantResult(tenant, resp.body)
	}
	return c.waitTask(ctx, device, tenant, accepted.ID)
}

func (c *Client) waitTask(ctx context.Context, device, tenant, id string) (DeclareResult, error) {
	ticker := time.NewTicker(c.opts.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return DeclareResult{}, &Error{Message: fmt.Sprintf("AS3 task %s still running after %s", id, c.opts.RequestTimeout), timeout: true}
			}
			return DeclareResult{}, &Error{Message: fmt.Sprintf("waiting for AS3 task %s: %s", id, ctx.Err())}
		case <-ticker.C:
		}
		resp, err := c.do(ctx, device, http.MethodGet, "/mgmt/shared/appsvcs/task/"+id, nil)
		if err != nil {
			return DeclareResult{}, err
		}
		var task struct {
			Results []as3Result `json:"results"`
		}
		if err := json.Unmarshal(resp.body, &task); err != nil {
			return DeclareResult{}, &Error{Status: resp.status, Message: "reading AS3 task " + id + ": " + err.Error()}
		}
		running := slices.ContainsFunc(task.Results, func(r as3Result) bool { return r.Message == "in progress" })
		if !running {
			return tenantResult(tenant, resp.body)
		}
	}
}

// tenantResult reads the result of a tenant in an AS3 answer, the only one when AS3 doesn't name the tenant.
func tenantResult(tenant string, body []byte) (DeclareResult, error) {
	var answer struct {
		Results []as3Result `json:"results"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || len(answer.Results) == 0 {
		return DeclareResult{}, &Error{Message: "AS3 answer holds no result: " + truncate(string(body))}
	}
	result := answer.Results[0]
	for _, r := range answer.Results {
		if r.Tenant == tenant {
			result = r
		}
	}
	if result.Code >= 400 {
		return DeclareResult{}, resultError(result)
	}
	return DeclareResult{Changed: result.Message != "no change", Message: result.Message}, nil
}

// DeleteTenant removes an AS3 tenant and everything in it, its partition included. A tenant already gone is fine.
func (c *Client) DeleteTenant(ctx context.Context, tenant string) error {
	ctx, cancel := context.WithTimeout(ctx, c.opts.RequestTimeout)
	defer cancel()
	device, err := c.device(ctx)
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, device, http.MethodDelete, "/mgmt/shared/appsvcs/declare/"+tenant, nil)
	var bigipErr *Error
	if errors.As(err, &bigipErr) && bigipErr.Status == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tenantResult(tenant, resp.body)
	return err
}

// ListTenants gives the remark of every AS3 tenant of the device.
func (c *Client) ListTenants(ctx context.Context) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.RequestTimeout)
	defer cancel()
	device, err := c.device(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, device, http.MethodGet, "/mgmt/shared/appsvcs/declare", nil)
	if err != nil {
		return nil, err
	}
	tenants := map[string]string{}
	// AS3 answers 204 when it holds no declaration
	if resp.status == http.StatusNoContent || len(resp.body) == 0 {
		return tenants, nil
	}
	var declaration map[string]json.RawMessage
	if err := json.Unmarshal(resp.body, &declaration); err != nil {
		return nil, &Error{Status: resp.status, Message: "reading the AS3 declaration: " + err.Error()}
	}
	for name, raw := range declaration {
		var obj struct {
			Class  string `json:"class"`
			Remark string `json:"remark"`
		}
		if json.Unmarshal(raw, &obj) == nil && obj.Class == "Tenant" {
			tenants[name] = obj.Remark
		}
	}
	return tenants, nil
}
