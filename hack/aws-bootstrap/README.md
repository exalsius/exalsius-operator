# AWS K3s Cluster Bootstrap

## Overview
This directory contains an Ansible playbook for automated deployment of a two-node Kubernetes cluster on AWS using k3s.

## Prerequisites

### AWS Credentials Configuration
Create a `credentials.yml` file in this directory with the following configuration:

```yaml
aws_access_key: XXXXXXXXXXX
aws_secret_key: XXXXXXXXXX
aws_region: eu-central-1
aws_image_ami_id: ami-0ca03f7e7f0df9d7a  # Ubuntu 24.04 LTS
aws_keyname: exalsius                     # AWS SSH key pair name
```

### SSH Configuration
Update the `private_key_file` path in `ansible.cfg` to point to your AWS SSH private key.

## Notes
- Ensure your AWS credentials have appropriate permissions for EC2 instance management
- The playbook is configured to use Ubuntu 24.04 LTS as the base image

## Accessing the Cluster
The kubeconfig file `k3s.kubeconfig` in the `fetched/` directory can be used to check and manage the exalsius management cluster installation, e.g. with `kubectl --kubeconfig fetched/k3s.kubeconfig get pods -A`
