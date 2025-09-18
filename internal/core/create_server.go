package core

import (
	"context"
	"errors"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// Change this if needed in the future when
// Hetzner deprecates Ubuntu 20.04 LTS, or if it
// is that time of the year.
const (
	TargetImage = "ubuntu-24.04"

	// Most Stable, Reliable and Cheapest
	// Location by Hetzner
	TargetLocation = "nbg1"
)

func CreateServer(client *hcloud.Client, server *hcloud.ServerType, serverName string) (*hcloud.Server, error) {
	// Get Server Image
	serverImage, _, err := client.Image.Get(
		context.Background(),
		TargetImage,
	)
	if err != nil {
		return nil, err
	}

	// Get ham-ssh-key SSH Key
	sshKey, _, err := client.SSHKey.Get(
		context.Background(),
		"ham-ssh-key",
	)
	if err != nil {
		return nil, err
	}

	defKey, _, err := client.SSHKey.Get(
		context.Background(),
		"default",
	)

	sshList := []*hcloud.SSHKey{sshKey}
	if err != nil {
		sshList = append(sshList, defKey)
	}

	// Get Location
	location, _, err := client.Location.Get(
		context.Background(),
		TargetLocation,
	)
	if err != nil {
		return nil, err
	}

	startAfterCreate := true
	automountVol := false

	// We need Special Volume of Size 400 GB
	// to hold only the lineage os build,
	// this will future proof this app.
	volCreateOpts := hcloud.VolumeCreateOpts{
		Name:      serverName + "-vol",
		Size:      400,
		Location:  location,
		Automount: &automountVol,
	}

	err = volCreateOpts.Validate()
	if err != nil {
		return nil, err
	}

	// Create Volume of 400 GiB
	volCreateResult, _, err := client.Volume.Create(
		context.Background(),
		volCreateOpts,
	)

	if err != nil {
		return nil, err
	}

	// Action Status and Error
	ok := false
	errMsg := ""

	// Check Current Action First
	checkAction(client, volCreateResult.Action, &ok, &errMsg)
	if !ok {
		return nil, errors.New(errMsg)
	}

	// Server Creation Options
	serverCreateOpts := hcloud.ServerCreateOpts{
		Name:             serverName,
		ServerType:       server,
		Image:            serverImage,
		SSHKeys:          sshList,
		Location:         location,
		StartAfterCreate: &startAfterCreate,
		Labels:           map[string]string{},
		PublicNet: &hcloud.ServerCreatePublicNet{
			EnableIPv4: true,
			EnableIPv6: false,
		},
		Volumes: []*hcloud.Volume{volCreateResult.Volume},
	}

	err = serverCreateOpts.Validate()
	if err != nil {
		// Destroy all Volumes we created before\
		_, volErr := client.Volume.Delete(
			context.Background(),
			volCreateResult.Volume,
		)

		if volErr != nil {
			return nil, err
		}

		return nil, err
	}

	// Create Server at Hetzner
	createResult, _, err := client.Server.Create(
		context.Background(),
		serverCreateOpts,
	)

	if err != nil {

		// Destroy all Volumes we created before\
		_, volErr := client.Volume.Delete(
			context.Background(),
			volCreateResult.Volume,
		)

		if volErr != nil {
			return nil, err
		}

		return nil, err
	}

	// Wait till we Success or Failure
	// result from Action that is currently
	// running.
	ok = false
	errMsg = ""

	// Check Current Action First
	checkAction(client, createResult.Action, &ok, &errMsg)
	if !ok {
		// Destroy all Volumes we created before\
		_, volErr := client.Volume.Delete(
			context.Background(),
			volCreateResult.Volume,
		)

		if volErr != nil {
			return nil, err
		}

		return nil, errors.New(errMsg)
	}

	return createResult.Server, nil
}

func checkAction(client *hcloud.Client, action *hcloud.Action, ok *bool, errMsg *string) {
	*ok = false
	*errMsg = ""
	targetAction := action
	var err error
	for {
		if targetAction == nil {
			*ok = true
			break
		}

		if targetAction.Status == hcloud.ActionStatusRunning {
			time.Sleep(time.Second * time.Duration(2))
			targetAction, _, err = client.Action.GetByID(
				context.Background(),
				targetAction.ID,
			)
			if err != nil {
				*ok = false
				*errMsg = err.Error()
				break
			}
			continue
		} else if targetAction.Status == hcloud.ActionStatusSuccess {
			*ok = true
		} else if targetAction.Status == hcloud.ActionStatusError {
			*ok = false
			*errMsg = "Action Failed (" + action.ErrorMessage + ")"
		}
		break
	}
}
