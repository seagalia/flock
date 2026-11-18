package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	"google.golang.org/protobuf/proto"
)

type CreateCertificate struct {
	File     string   `short:"f" name:"file" help:"Path to a json or yml file containing the certificate to create, the other flags override its fields" type:"path" predictor:"file"`
	Name     string   `short:"n" name:"name" help:"Name of the certificate"`
	CertFile string   `short:"c" name:"cert" help:"Certificate file" type:"path" predictor:"file"`
	KeyFile  string   `short:"k" name:"key" help:"Key file" type:"path" predictor:"file"`
	Ca       string   `short:"a" name:"ca" help:"CA file" type:"path" predictor:"file"`
	Domains  []string `short:"d" name:"domains" sep:"none" help:"Domains"`
	Force    bool     `name:"force" help:"Force create certificate without confirmation"`
	Json     bool     `short:"j" name:"json" help:"Format in json instead of human table readable."`

	client *clients.Client
}

func (c *CreateCertificate) SetClient(client *clients.Client) {
	c.client = client
}

func (c *CreateCertificate) Run(ctx context.Context) error {
	certificate, err := c.certificateFromFile()
	if err != nil {
		return err
	}
	err = c.applyFlags(certificate)
	if err != nil {
		return err
	}
	if certificate.GetName() == "" {
		return errors.New("the certificate needs a name, with --name or in the file")
	}

	if err := refuseLockAdded(nil, certificate.GetMetadata()); err != nil {
		return err
	}
	if err := resolveSecretRefs(ctx, c.client, certificate); err != nil {
		return err
	}

	// the diff goes to the terminal, the private key stays out of it
	shown := proto.Clone(certificate).(*cert.Certificate)
	if shown.GetPrivateKey() != "" {
		shown.PrivateKey = "<hidden>"
	}
	confirm, err := DiffAndConfirm(nil, shown, c.Force)
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	response, err := c.client.Certificate().CreateCertificate(ctx, &svccert.CreateCertificateRequest{
		Certificate: certificate,
	})
	if err != nil {
		return err
	}
	if c.Json {
		return PrintProtoJson(response.GetCertificate())
	}
	msg.Successf("Certificate %s successfully.", msg.Green("created"))
	return nil
}

func (c *CreateCertificate) certificateFromFile() (*cert.Certificate, error) {
	if c.File == "" {
		return &cert.Certificate{}, nil
	}
	certificate, loaded, err := FileToProto[*cert.Certificate](c.File)
	if err != nil {
		return nil, err
	}
	if !loaded {
		return nil, fmt.Errorf("file %s is empty or missing", msg.Cyan(c.File))
	}
	certificate.UUID = ""
	return certificate, nil
}

func (c *CreateCertificate) applyFlags(certificate *cert.Certificate) error {
	if c.Name != "" {
		certificate.Name = c.Name
	}
	if len(c.Domains) > 0 {
		certificate.DomainNames = c.Domains
	}
	if c.CertFile != "" {
		certChain, err := os.ReadFile(c.CertFile)
		if err != nil {
			return fmt.Errorf("error when reading certificate file: %w", err)
		}
		certificate.CertChain = string(certChain)
	}
	if c.KeyFile != "" {
		privateKey, err := os.ReadFile(c.KeyFile)
		if err != nil {
			return fmt.Errorf("error when reading key file: %w", err)
		}
		certificate.PrivateKey = string(privateKey)
	}
	if c.Ca != "" {
		ca, err := os.ReadFile(c.Ca)
		if err != nil {
			return fmt.Errorf("error when reading ca file: %w", err)
		}
		certificate.Ca = string(ca)
	}
	return nil
}
