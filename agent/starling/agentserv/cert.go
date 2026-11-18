package agentserv

import (
	"context"
	"encoding/pem"
	"os"

	"connectrpc.com/connect/v2"
	agentmgmt "github.com/seagalia/flock/sdk/flock/services/agent/manage/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *AgentServer) UpdateServerCertificate(ctx context.Context, req *agentmgmt.UpdateServerCertificateRequest) (*emptypb.Empty, error) {
	if req.GetCa() == "" && req.GetCertChain() == "" && req.GetPrivateKey() == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid request: no certificate or CA provided")
	}
	err := s.checkCertificateRequest(req)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid request: %v", err)
	}

	err = s.updateCACert(req.GetCa())
	if err != nil {
		return nil, err
	}

	err = s.updatePair(req.GetCertChain(), req.GetPrivateKey())
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *AgentServer) updateCACert(ca string) error {
	if ca == "" || s.tlsInfo.RootCaPool == nil {
		return nil
	}
	err := os.WriteFile(s.tlsInfo.CAFilePath, []byte(ca), 0644)
	if err != nil {
		return connect.Errorf(connect.CodeInternal, "failed to write ca file: %v", err)
	}
	return nil
}

func (s *AgentServer) updatePair(cert, key string) error {
	if cert == "" && key == "" {
		return nil
	}
	if cert != "" && key == "" || cert == "" && key != "" {
		return connect.Errorf(connect.CodeInvalidArgument, "invalid request: cert and key must be provided together")
	}
	err := os.WriteFile(s.tlsInfo.CertFilePath, []byte(cert), 0644)
	if err != nil {
		return connect.Errorf(connect.CodeInternal, "failed to write cert file: %v", err)
	}
	// private key must only be readable by the agent
	err = os.WriteFile(s.tlsInfo.KeyFilePath, []byte(key), 0600)
	if err != nil {
		return connect.Errorf(connect.CodeInternal, "failed to write key file: %v", err)
	}
	return nil
}

func (s *AgentServer) checkCertificateRequest(req *agentmgmt.UpdateServerCertificateRequest) error {

	ca := req.GetCa()
	for ca != "" {
		var block *pem.Block
		var rest []byte
		block, rest = pem.Decode([]byte(ca))
		if block == nil {
			return connect.Errorf(connect.CodeInvalidArgument, "invalid ca")
		}
		ca = string(rest)
	}

	chain := req.GetCertChain()
	for chain != "" {
		var block *pem.Block
		var rest []byte
		block, rest = pem.Decode([]byte(chain))
		if block == nil {
			return connect.Errorf(connect.CodeInvalidArgument, "invalid certChain")
		}
		chain = string(rest)
	}

	key := req.GetPrivateKey()
	for key != "" {
		var block *pem.Block
		var rest []byte
		block, rest = pem.Decode([]byte(key))
		if block == nil {
			return connect.Errorf(connect.CodeInvalidArgument, "invalid privateKey")
		}
		key = string(rest)
	}

	return nil
}
