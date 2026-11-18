package cli

import (
	"context"
	"fmt"
	"os"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
)

type UpdateCertificate struct {
	Named CertNamed `arg:"" name:"certificate" help:"certificate name or uuid" predictor:"certificate"`

	CertFile string   `short:"c" name:"cert" help:"Certificate file" required:"" type:"path" predictor:"file"`
	KeyFile  string   `short:"k" name:"key" help:"Key file" required:"" type:"path" predictor:"file"`
	Ca       string   `short:"a" name:"ca" help:"CA file" type:"path" predictor:"file"`
	Name     string   `short:"n" name:"name" help:"Name"`
	Domains  []string `short:"d" name:"domains" sep:"none" help:"Domains"`
	Force    bool     `name:"force" help:"Force update certificate without confirmation"`

	client *clients.Client
}

func (c *UpdateCertificate) SetClient(client *clients.Client) {
	c.client = client
}

func (c *UpdateCertificate) Run(ctx context.Context) error {
	current, err := FindCertificate(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLocked("certificate", current.GetName(), current.GetMetadata()); err != nil {
		return err
	}

	certChain, err := os.ReadFile(c.CertFile)
	if err != nil {
		return fmt.Errorf("error when reading certificate file: %w", err)
	}
	privateKey, err := os.ReadFile(c.KeyFile)
	if err != nil {
		return fmt.Errorf("error when reading key file: %w", err)
	}
	var ca []byte
	if c.Ca != "" {
		ca, err = os.ReadFile(c.Ca)
		if err != nil {
			return fmt.Errorf("error when reading ca file: %w", err)
		}
	}

	name := current.Name
	if c.Name != "" {
		name = c.Name
	}
	newCert := &cert.Certificate{
		UUID:        current.GetUUID(),
		Name:        name,
		CertChain:   string(certChain),
		PrivateKey:  string(privateKey),
		Ca:          string(ca),
		DomainNames: c.Domains,
	}

	confirm, err := DiffAndConfirm(current, newCert, c.Force)
	if err != nil {
		return err
	}

	if !confirm {
		return nil
	}

	_, err = c.client.Certificate().UpdateCertificate(ctx, &svccert.UpdateCertificateRequest{
		UUID: current.GetUUID(),
		Certificate: &cert.Certificate{
			Name:        name,
			CertChain:   string(certChain),
			PrivateKey:  string(privateKey),
			Ca:          string(ca),
			DomainNames: c.Domains,
		},
	})
	if err != nil {
		return err
	}

	msg.Success("Certificate updated successfully.")
	return nil
}
