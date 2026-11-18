package serves

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/observe"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *FlockServer) CreateCertificate(ctx context.Context, req *svccert.CreateCertificateRequest) (*svccert.CertificateResponse, error) {
	certInfo := req.GetCertificate()
	certInfo.CertChain = strings.TrimSpace(certInfo.CertChain)
	certInfo.PrivateKey = strings.TrimSpace(certInfo.PrivateKey)
	certInfo.Ca = strings.TrimSpace(certInfo.Ca)
	certInfo.UUID = uuid.New().String()

	err := s.checkCertificateRequest(certInfo)
	if err != nil {
		return nil, err
	}

	sig, err := helpers.MessageSignature(certInfo)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not create signature: %v", err).WithCause(err)
	}

	err = s.checkSecretRefs(ctx, nil, certInfo)
	if err != nil {
		return nil, err
	}

	err = s.storeMan.Certificates().Create(ctx, certInfo)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	// the certificate is stored: what follows completes the change, it must not stop halfway when the client leaves
	ctx, cancel := detachedContext(ctx)
	defer cancel()

	err = s.addPermForUser(ctx, helpers.TypeUrl[*cert.Certificate](), certInfo.UUID)
	if err != nil {
		return nil, err
	}

	observe.EmitSubject(observer.EventType_CREATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrlFromMessage(certInfo),
		Identifier: certInfo.UUID,
		Signature:  sig,
	})

	certInfo.PrivateKey = ""
	return &svccert.CertificateResponse{
		Certificate: certInfo,
	}, nil
}

func (s *FlockServer) checkCertificateRequest(certInfo *cert.Certificate) error {

	ca := certInfo.GetCa()
	for ca != "" {
		var block *pem.Block
		var rest []byte
		block, rest = pem.Decode([]byte(ca))
		if block == nil {
			return connect.Errorf(connect.CodeInvalidArgument, "invalid ca")
		}
		ca = string(rest)
	}

	chain := certInfo.GetCertChain()
	for chain != "" {
		var block *pem.Block
		var rest []byte
		block, rest = pem.Decode([]byte(chain))
		if block == nil {
			return connect.Errorf(connect.CodeInvalidArgument, "invalid certChain")
		}
		chain = string(rest)
	}

	key := certInfo.GetPrivateKey()
	for key != "" {
		var block *pem.Block
		var rest []byte
		block, rest = pem.Decode([]byte(key))
		if block == nil {
			return connect.Errorf(connect.CodeInvalidArgument, "invalid privateKey")
		}
		key = string(rest)
	}

	if certInfo.GetCertChain() != "" || certInfo.GetPrivateKey() != "" {
		_, err := tls.X509KeyPair([]byte(certInfo.GetCertChain()), []byte(certInfo.GetPrivateKey()))
		if err != nil {
			return connect.Errorf(connect.CodeInvalidArgument, "invalid certChain or privateKey: %s", err.Error())
		}
	}

	return nil
}

func (s *FlockServer) ReadCertificate(ctx context.Context, req *svccore.UuidRequest) (*svccert.CertificateResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*cert.Certificate](), req.GetUUID(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	certInfo, err := s.storeMan.Certificates().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	certInfo.PrivateKey = ""

	return &svccert.CertificateResponse{
		Certificate: certInfo,
	}, nil
}

func (s *FlockServer) UpdateCertificate(ctx context.Context, req *svccert.UpdateCertificateRequest) (*svccert.CertificateResponse, error) {
	certInfo := req.GetCertificate()
	certInfo.CertChain = strings.TrimSpace(certInfo.CertChain)
	certInfo.PrivateKey = strings.TrimSpace(certInfo.PrivateKey)
	certInfo.Ca = strings.TrimSpace(certInfo.Ca)

	err := s.checkPermission(ctx, helpers.TypeUrl[*cert.Certificate](), req.GetUUID(), permission.Role_WRITER)
	if err != nil {
		return nil, err
	}

	certInfo.UUID = req.GetUUID()

	keyKeptFromStore := certInfo.PrivateKey == ""
	if keyKeptFromStore {
		stored, err := s.storeMan.Certificates().Read(ctx, req.GetUUID())
		if err != nil {
			return nil, errors.ToConnectError(err)
		}
		certInfo.PrivateKey = stored.GetPrivateKey()
	}

	err = s.checkCertificateRequest(certInfo)
	if err != nil {
		return nil, err
	}

	err = checkNameChangedByOwner[*cert.Certificate](ctx, s.storeMan, certInfo, func() error {
		return s.checkPermission(ctx, helpers.TypeUrl[*cert.Certificate](), req.GetUUID(), permission.Role_OWNER)
	})
	if err != nil {
		return nil, err
	}

	sig, err := helpers.MessageSignature(certInfo)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not create signature: %v", err).WithCause(err)
	}

	storedCert, err := s.storeMan.Certificates().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	err = s.checkSecretRefs(ctx, storedCert, certInfo)
	if err != nil {
		return nil, err
	}

	err = s.storeMan.Certificates().Update(ctx, req.GetUUID(), certInfo)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to update certificate: %v", err).WithCause(err)
	}

	s.spreadPermToMembers(ctx, helpers.TypeUrl[*cert.Certificate](), certInfo.UUID)

	observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrlFromMessage(certInfo),
		Identifier: certInfo.UUID,
		Signature:  sig,
	})

	certInfo.PrivateKey = ""

	return &svccert.CertificateResponse{
		Certificate: certInfo,
	}, nil
}

func (s *FlockServer) DeleteCertificate(ctx context.Context, req *svccore.UuidRequest) (*emptypb.Empty, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*cert.Certificate](), req.GetUUID(), permission.Role_OWNER)
	if err != nil {
		return nil, err
	}

	foundVss, err := s.storeMan.VirtualServers().FindSrcWithForeignResource(ctx, helpers.TypeUrl[*cert.Certificate](), req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	if len(foundVss) > 0 {
		return nil, connect.Errorf(connect.CodeFailedPrecondition,
			"certificate %s is used by virtual server(s) %s .", req.GetUUID(), strings.Join(foundVss, ", "),
		)
	}

	err = s.storeMan.Certificates().Delete(ctx, req.GetUUID())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to delete certificate: %v", err).WithCause(err)
	}

	observe.EmitSubject(observer.EventType_DELETE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrl[*cert.Certificate](),
		Identifier: req.GetUUID(),
	})
	return &emptypb.Empty{}, nil
}

func (s *FlockServer) ListCertificate(ctx context.Context, req *svccert.ListCertificateRequest) (*svccert.ListCertificateResponse, error) {
	certs, err := s.storeMan.Certificates().List(ctx, s.makeListOptions(ctx, req), req.GetFilters()...)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to list certificates: %v", err).WithCause(err)
	}

	for _, certInfo := range certs {
		certInfo.PrivateKey = ""
	}

	return &svccert.ListCertificateResponse{
		Certificates: certs,
	}, nil
}
