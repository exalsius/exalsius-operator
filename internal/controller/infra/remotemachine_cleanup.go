/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package infra

import (
	"context"
	"fmt"

	infrastructurev1beta1 "github.com/k0sproject/k0smotron/api/infrastructure/v1beta1"
	rig "github.com/k0sproject/rig/v2"
	rigssh "github.com/k0sproject/rig/v2/protocol/ssh"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CleanupRemoteMachineNetBird runs NetBird cleanup via SSH before k0smotron cleanup
// This function executes NetBird cleanup commands on a remote machine before k0smotron's
// finalizer logic runs, ensuring NetBird is properly stopped and cleaned up.
func CleanupRemoteMachineNetBird(ctx context.Context, c client.Client, rm *infrastructurev1beta1.RemoteMachine) error {
	log := log.FromContext(ctx)

	// Get SSH key from RemoteMachine spec
	sshKeySecret := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{
		Name:      rm.Spec.SSHKeyRef.Name,
		Namespace: rm.Namespace,
	}, sshKeySecret); err != nil {
		return fmt.Errorf("failed to get SSH key: %w", err)
	}

	sshKey := sshKeySecret.Data["value"]
	if len(sshKey) == 0 {
		return fmt.Errorf("SSH key is empty")
	}

	// Create SSH client (same approach as k0smotron)
	authM, err := rigssh.ParseSSHPrivateKey(sshKey, nil)
	if err != nil {
		return fmt.Errorf("failed to parse SSH key: %w", err)
	}

	config := rigssh.Config{
		Address:     rm.Spec.Address,
		Port:        rm.Spec.Port,
		User:        rm.Spec.User,
		AuthMethods: authM,
	}

	rigClient, err := rig.NewClient(rig.WithConnectionConfigurer(&config))
	if err != nil {
		return fmt.Errorf("failed to create SSH client: %w", err)
	}

	if err := rigClient.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to %s: %w", rm.Spec.Address, err)
	}
	defer rigClient.Disconnect()

	// Use sudo if needed (respects RemoteMachine.Spec.UseSudo, same as k0smotron)
	if rm.Spec.UseSudo {
		rigClient = rigClient.Sudo()
	}

	// NetBird cleanup commands
	// Using || true to ensure commands don't fail if NetBird is not installed/running
	commands := []string{
		"netbird deregister|| true",
		"apt-get remove -y netbird|| true",
		"rm /usr/local/bin/k0s-worker-wrapper.sh || true",
		"rm /usr/local/bin/k0s || true",
	}

	log.Info("Cleaning up NetBird on RemoteMachine", "machine", rm.Name, "address", rm.Spec.Address)

	for _, cmd := range commands {
		output, err := rigClient.ExecOutput(cmd)
		if err != nil {
			// Log but continue - we want to try all cleanup steps
			log.Error(err, "NetBird cleanup command failed (continuing)",
				"command", cmd,
				"output", output,
				"machine", rm.Name)
		} else {
			log.V(1).Info("NetBird cleanup command succeeded",
				"command", cmd,
				"output", output,
				"machine", rm.Name)
		}
	}

	log.Info("NetBird cleanup completed", "machine", rm.Name)
	return nil
}
