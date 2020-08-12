package planfile

import (
	"archive/zip"
	"fmt"
	"github.com/hashicorp/terraform/configs"
	"github.com/hashicorp/terraform/terraform"
	"os"
	"time"

	"github.com/hashicorp/terraform/command/jsonplan"
	"github.com/hashicorp/terraform/configs/configload"
	"github.com/hashicorp/terraform/plans"
	"github.com/hashicorp/terraform/states/statefile"
)

// Create creates a new plan file with the given filename, overwriting any
// file that might already exist there.
//
// A plan file contains both a snapshot of the configuration and of the latest
// state file in addition to the plan itself, so that Terraform can detect
// if the world has changed since the plan was created and thus refuse to
// apply it.
func Create(filename string, configSnap *configload.Snapshot, stateFile *statefile.File, plan *plans.Plan) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	// tfplan file
	{
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:     tfplanFilename,
			Method:   zip.Deflate,
			Modified: time.Now(),
		})
		if err != nil {
			return fmt.Errorf("failed to create tfplan file: %s", err)
		}
		err = writeTfplan(plan, w)
		if err != nil {
			return fmt.Errorf("failed to write plan: %s", err)
		}
	}

	// tfstate file
	{
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:     tfstateFilename,
			Method:   zip.Deflate,
			Modified: time.Now(),
		})
		if err != nil {
			return fmt.Errorf("failed to create embedded tfstate file: %s", err)
		}
		err = statefile.Write(stateFile, w)
		if err != nil {
			return fmt.Errorf("failed to write state snapshot: %s", err)
		}
	}

	// tfconfig directory
	{
		err := writeConfigSnapshot(configSnap, zw)
		if err != nil {
			return fmt.Errorf("failed to write config snapshot: %s", err)
		}
	}

	return nil
}

// CreateJson creates a new plan file as JSON with the given filename, overwriting any
// file that might already exist there.
//
func CreateJson(
	filename string,
	config *configs.Config,
	stateFile *statefile.File,
	plan *plans.Plan,
	schemas *terraform.Schemas,
) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	{
		b, err := jsonplan.Marshal(config, plan, stateFile, schemas)
		if err != nil {
			return fmt.Errorf("failed to marshal tfplan as JSON: %s", err)
		}
		_, err = f.Write(b)
		if err != nil {
			return fmt.Errorf("failed to write plan as JSON: %s", err)
		}
	}

	return nil
}
