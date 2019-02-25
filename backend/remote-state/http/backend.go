package http

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"reflect"

	cleanhttp "github.com/hashicorp/go-cleanhttp"
	"github.com/hashicorp/terraform/backend"
	"github.com/hashicorp/terraform/helper/pathorcontents"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/hashicorp/terraform/state"
	"github.com/hashicorp/terraform/state/remote"
	"github.com/youmark/pkcs8"
)

func New() backend.Backend {
	s := &schema.Backend{
		Schema: map[string]*schema.Schema{
			"address": &schema.Schema{
				Type:        schema.TypeString,
				Required:    true,
				Description: "The address of the REST endpoint",
			},
			"update_method": &schema.Schema{
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "POST",
				Description: "HTTP method to use when updating state",
			},
			"lock_address": &schema.Schema{
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The address of the lock REST endpoint",
			},
			"unlock_address": &schema.Schema{
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The address of the unlock REST endpoint",
			},
			"lock_method": &schema.Schema{
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "LOCK",
				Description: "The HTTP method to use when locking",
			},
			"unlock_method": &schema.Schema{
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "UNLOCK",
				Description: "The HTTP method to use when unlocking",
			},
			"username": &schema.Schema{
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The username for HTTP basic authentication",
			},
			"password": &schema.Schema{
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The password for HTTP basic authentication",
			},
			"skip_cert_verification": &schema.Schema{
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Whether to skip TLS verification.",
			},
			"tls_client_cert": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("HTTP_BACKEND_TLS_CLIENT_CERT", ""),
				Description: "The client certificate used for authentication. Path to file or contents.",
			},
			"tls_client_key": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("HTTP_BACKEND_TLS_CLIENT_KEY", ""),
				Description: "The client key used for authentication. Path to file or contents.",
			},
			"tls_client_key_password": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("HTTP_BACKEND_TLS_CLIENT_KEY_PASSWORD", ""),
				Description: "The password for the client key used for authentication. If present and client key not encrypted it will fail",
			},
			"tls_client_ca": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "",
				DefaultFunc: schema.EnvDefaultFunc("HTTP_BACKEND_TLS_CLIENT_CA", ""),
				Description: "The CAs that the client trusts by default. Path to file or contents.",
			},
		},
	}

	b := &Backend{Backend: s}
	b.Backend.ConfigureFunc = b.configure
	return b
}

type Backend struct {
	*schema.Backend

	client *httpClient
}

func (b *Backend) configure(ctx context.Context) error {
	data := schema.FromContextBackendConfig(ctx)

	address := data.Get("address").(string)
	updateURL, err := url.Parse(address)
	if err != nil {
		return fmt.Errorf("failed to parse address URL: %s", err)
	}
	if updateURL.Scheme != "http" && updateURL.Scheme != "https" {
		return fmt.Errorf("address must be HTTP or HTTPS")
	}

	updateMethod := data.Get("update_method").(string)

	var lockURL *url.URL
	if v, ok := data.GetOk("lock_address"); ok && v.(string) != "" {
		var err error
		lockURL, err = url.Parse(v.(string))
		if err != nil {
			return fmt.Errorf("failed to parse lockAddress URL: %s", err)
		}
		if lockURL.Scheme != "http" && lockURL.Scheme != "https" {
			return fmt.Errorf("lockAddress must be HTTP or HTTPS")
		}
	}

	lockMethod := data.Get("lock_method").(string)

	var unlockURL *url.URL
	if v, ok := data.GetOk("unlock_address"); ok && v.(string) != "" {
		var err error
		unlockURL, err = url.Parse(v.(string))
		if err != nil {
			return fmt.Errorf("failed to parse unlockAddress URL: %s", err)
		}
		if unlockURL.Scheme != "http" && unlockURL.Scheme != "https" {
			return fmt.Errorf("unlockAddress must be HTTP or HTTPS")
		}
	}

	unlockMethod := data.Get("unlock_method").(string)

	client := cleanhttp.DefaultPooledClient()

	if data.Get("skip_cert_verification").(bool) {
		// ignores TLS verification
		client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}
	clientCert := data.Get("tls_client_cert").(string)
	if clientCert != "" {
		config := &tls.Config{}
		// Has TLS client cert
		clientKey := data.Get("tls_client_key").(string)
		if clientKey == "" {
			return fmt.Errorf("If client cert is present, client key must be too")
		}
		clientCA := data.Get("tls_client_ca").(string)
		caCertPool, err := x509.SystemCertPool()

		if err != nil {
			caCertPool = x509.NewCertPool()
		}
		if clientCA != "" {
			clientCAContents, wasPath, err := pathorcontents.Read(clientCA)
			if err != nil {
				if wasPath {
					return fmt.Errorf("error reading certificate: %s", clientCA)
				}
			} else {
				caCertPool.AppendCertsFromPEM([]byte(clientCAContents))
			}
		}
		config.RootCAs = caCertPool
		clientKeyContents, wasPath, err := pathorcontents.Read(clientKey)
		if err != nil {
			if wasPath {
				return fmt.Errorf("error reading key from file %s", clientKey)
			} else {
				return fmt.Errorf("error reading key %+v", err)
			}
		}
		clientCertContents, wasPath, err := pathorcontents.Read(clientCert)
		if err != nil {
			if wasPath {
				return fmt.Errorf("error reading cert from file %s %+v", clientCert, err)
			} else {
				return fmt.Errorf("error reading certificate %+v", err)
			}
		}

		block, r := pem.Decode([]byte(clientKeyContents))
		if block == nil {
			return fmt.Errorf("error decoding client key. Not a valid key. Rest: %+v", r)
		}
		clientKeyPassword := data.Get("tls_client_key_password").(string)
		var certPair tls.Certificate
		if block.Type == "ENCRYPTED PRIVATE KEY" || x509.IsEncryptedPEMBlock(block) {
			var key interface{}
			var err error
			key, err = pkcs8.ParsePKCS8PrivateKey(block.Bytes, []byte(clientKeyPassword))
			var pemData []byte
			if err == nil {
				switch key.(type) {
				case *rsa.PrivateKey:
					rsaKey, ok := key.(*rsa.PrivateKey)
					if !ok {
						return fmt.Errorf("Error casting key to rsa.PrivateKey. Typeof key: %s", reflect.TypeOf(key))
					}
					pkcs8Key, err := x509.MarshalPKCS8PrivateKey(rsaKey)
					if err != nil {
						return fmt.Errorf("Error marshalling key %+v", err)
					}
					pemData = pem.EncodeToMemory(
						&pem.Block{
							Type:  "RSA PRIVATE KEY",
							Bytes: pkcs8Key,
						},
					)
				case *ecdsa.PrivateKey:
					ecdsaKey, ok := key.(*ecdsa.PrivateKey)
					if !ok {
						return fmt.Errorf("Error casting key to ecdsa.PrivateKey. Typeof key: %s", reflect.TypeOf(key))
					}
					pkcs8Key, err := x509.MarshalECPrivateKey(ecdsaKey)
					if err != nil {
						return fmt.Errorf("Error marshalling key %+v", err)
					}
					pemData = pem.EncodeToMemory(
						&pem.Block{
							Type:  "PRIVATE KEY",
							Bytes: pkcs8Key,
						},
					)

				default:
					return fmt.Errorf("unsupported type %+v", reflect.TypeOf(key))
				}
				certPair, err = tls.X509KeyPair([]byte(clientCertContents), pemData)
				if err != nil {
					return fmt.Errorf("Error parsing a public/private key pair from pkcs8 encrypted block: %s", err)
				}
			} else {
				der, err := x509.DecryptPEMBlock(block, []byte(clientKeyPassword))
				if err != nil {
					return fmt.Errorf("decrypt failed: %+v", err)
				}
				pkcs1Key, err := x509.ParsePKCS1PrivateKey(der)
				if err != nil {
					return fmt.Errorf("Error marshalling key %+v", err)
				}
				pkcs1KeyBytes := x509.MarshalPKCS1PrivateKey(pkcs1Key)
				pemData = pem.EncodeToMemory(
					&pem.Block{
						Type:    "RSA PRIVATE KEY",
						Bytes:   pkcs1KeyBytes,
						Headers: block.Headers,
					},
				)
				if err != nil {
					return fmt.Errorf("invalid private key: %+v", err)
				}
				certPair, err = tls.X509KeyPair([]byte(clientCertContents), pemData)
				if err != nil {
					return fmt.Errorf("error parsing certificate + encrypted pkcs1 key %+v", err)
				}
			}
		} else {
			certPair, err = tls.X509KeyPair([]byte(clientCert), []byte(clientKey))
			if err != nil {
				return fmt.Errorf("error parsing certificates %+v . Block: %#v", err, block.Headers)
			}
		}
		config.Certificates = []tls.Certificate{certPair}
		config.BuildNameToCertificate()
		client.Transport.(*http.Transport).TLSClientConfig = config
	}

	b.client = &httpClient{
		URL:          updateURL,
		UpdateMethod: updateMethod,

		LockURL:      lockURL,
		LockMethod:   lockMethod,
		UnlockURL:    unlockURL,
		UnlockMethod: unlockMethod,

		Username: data.Get("username").(string),
		Password: data.Get("password").(string),

		// accessible only for testing use
		Client: client,
	}
	return nil
}

func (b *Backend) StateMgr(name string) (state.State, error) {
	if name != backend.DefaultStateName {
		return nil, backend.ErrWorkspacesNotSupported
	}

	return &remote.State{Client: b.client}, nil
}

func (b *Backend) Workspaces() ([]string, error) {
	return nil, backend.ErrWorkspacesNotSupported
}

func (b *Backend) DeleteWorkspace(string) error {
	return backend.ErrWorkspacesNotSupported
}
