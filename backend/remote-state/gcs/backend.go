// Package gcs implements remote storage of state on Google Cloud Storage (GCS).
package gcs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/hashicorp/terraform/backend"
	"github.com/hashicorp/terraform/helper/pathorcontents"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/hashicorp/terraform/terraform"
	"golang.org/x/oauth2/jwt"
	"google.golang.org/api/option"
)

// gcsBackend implements "backend".Backend for GCS.
// Input(), Validate() and Configure() are implemented by embedding *schema.Backend.
// State(), DeleteState() and States() are implemented explicitly.
type gcsBackend struct {
	*schema.Backend

	storageClient  *storage.Client
	storageContext context.Context

	bucketName       string
	prefix           string
	defaultStateFile string

	encryptionKey []byte

	projectID string
	region    string
}

func New() backend.Backend {
	be := &gcsBackend{}
	be.Backend = &schema.Backend{
		ConfigureFunc: be.configure,
		Schema: map[string]*schema.Schema{
			"bucket": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "The name of the Google Cloud Storage bucket",
			},

			"path": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Path of the default state file",
				Deprecated:  "Use the \"prefix\" option instead",
			},

			"prefix": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The directory where state files will be saved inside the bucket",
			},

			"credentials": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Google Cloud JSON Account Key",
				Default:     "",
			},

			"encryption_key": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "A 32 byte base64 encoded 'customer supplied encryption key' used to encrypt all state.",
				Default:     "",
			},

			"project": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Google Cloud Project ID",
				Default:     "",
			},

			"region": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Region / location in which to create the bucket",
				Default:     "",
			},
		},
	}

	return be
}

func (b *gcsBackend) configure(ctx context.Context) error {
	if b.storageClient != nil {
		return nil
	}

	// ctx is a background context with the backend config added.
	// Since no context is passed to remoteClient.Get(), .Lock(), etc. but
	// one is required for calling the GCP API, we're holding on to this
	// context here and re-use it later.
	b.storageContext = ctx

	data := schema.FromContextBackendConfig(b.storageContext)

	b.bucketName = data.Get("bucket").(string)
	b.prefix = strings.TrimLeft(data.Get("prefix").(string), "/")
	if b.prefix != "" && !strings.HasSuffix(b.prefix, "/") {
		b.prefix = b.prefix + "/"
	}

	b.defaultStateFile = strings.TrimLeft(data.Get("path").(string), "/")

	b.projectID = data.Get("project").(string)
	if id := os.Getenv("GOOGLE_PROJECT"); b.projectID == "" && id != "" {
		b.projectID = id
	}
	b.region = data.Get("region").(string)
	if r := os.Getenv("GOOGLE_REGION"); b.projectID == "" && r != "" {
		b.region = r
	}

	var opts []option.ClientOption

	creds := data.Get("credentials").(string)
	if creds == "" {
		creds = os.Getenv("GOOGLE_CREDENTIALS")
	}

	if creds != "" {
		var account accountFile

		// to mirror how the provider works, we accept the file path or the contents
		contents, _, err := pathorcontents.Read(creds)
		if err != nil {
			return fmt.Errorf("Error loading credentials: %s", err)
		}

		if err := json.Unmarshal([]byte(contents), &account); err != nil {
			return fmt.Errorf("Error parsing credentials '%s': %s", contents, err)
		}

		conf := jwt.Config{
			Email:      account.ClientEmail,
			PrivateKey: []byte(account.PrivateKey),
			Scopes:     []string{storage.ScopeReadWrite},
			TokenURL:   "https://accounts.google.com/o/oauth2/token",
		}

		opts = append(opts, option.WithHTTPClient(conf.Client(ctx)))
	} else {
		opts = append(opts, option.WithScopes(storage.ScopeReadWrite))
	}

	opts = append(opts, option.WithUserAgent(terraform.UserAgentString()))
	client, err := storage.NewClient(b.storageContext, opts...)
	if err != nil {
		return fmt.Errorf("storage.NewClient() failed: %v", err)
	}

	b.storageClient = client

	// If projectID is provider we will check and create the bucket if it does not exists.
	// projectID is only used when creating a new bucket during initialization.
	if b.projectID != "" {
		err = createBucketIfNotExist(b)
		if err != nil {
			return err
		}
	}

	key := data.Get("encryption_key").(string)
	if key == "" {
		key = os.Getenv("GOOGLE_ENCRYPTION_KEY")
	}

	if key != "" {
		kc, _, err := pathorcontents.Read(key)
		if err != nil {
			return fmt.Errorf("Error loading encryption key: %s", err)
		}

		// The GCS client expects a customer supplied encryption key to be
		// passed in as a 32 byte long byte slice. The byte slice is base64
		// encoded before being passed to the API. We take a base64 encoded key
		// to remain consistent with the GCS docs.
		// https://cloud.google.com/storage/docs/encryption#customer-supplied
		// https://github.com/GoogleCloudPlatform/google-cloud-go/blob/def681/storage/storage.go#L1181
		k, err := base64.StdEncoding.DecodeString(kc)
		if err != nil {
			return fmt.Errorf("Error decoding encryption key: %s", err)
		}
		b.encryptionKey = k
	}

	return nil
}

func createBucketIfNotExist(b *gcsBackend) error {
	bkt := b.storageClient.Bucket(b.bucketName)
	_, err := bkt.Attrs(b.storageContext)
	if err != nil {
		if err != storage.ErrBucketNotExist {
			return fmt.Errorf("Bucket %q is not accessible: %v", b.bucketName, err)
		}

		attrs := &storage.BucketAttrs{
			Location: b.region,
		}
		err := bkt.Create(b.storageContext, b.projectID, attrs)
		if err != nil {
			return fmt.Errorf("Bucket %q didn't exist and creating it failed: %v", b.bucketName, err)
		}
	}
	return nil
}

// accountFile represents the structure of the account file JSON file.
type accountFile struct {
	PrivateKeyId string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	ClientId     string `json:"client_id"`
}
