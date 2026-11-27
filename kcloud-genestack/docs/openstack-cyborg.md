# Deploy Cyborg

OpenStack Cyborg is the accelerator lifecycle management service in OpenStack.
It enables the management and scheduling of hardware accelerators such as GPUs,
FPGAs, and other specialized devices. This document outlines the deployment of
OpenStack Cyborg using Genestack.

## Create secrets

!!! note "Information about the secrets used"

    Manual secret generation is only required if you haven't run the
    `create-secrets.sh` script located in `/opt/genestack/bin`.

    ??? example "Example secret generation"

        ``` shell
        kubectl --namespace openstack \
                create secret generic cyborg-rabbitmq-password \
                --type Opaque \
                --from-literal=username="cyborg" \
                --from-literal=password="$(< /dev/urandom tr -dc _A-Za-z0-9 | head -c${1:-64};echo;)"
        kubectl --namespace openstack \
                create secret generic cyborg-db-password \
                --type Opaque \
                --from-literal=password="$(< /dev/urandom tr -dc _A-Za-z0-9 | head -c${1:-32};echo;)"
        kubectl --namespace openstack \
                create secret generic cyborg-admin \
                --type Opaque \
                --from-literal=password="$(< /dev/urandom tr -dc _A-Za-z0-9 | head -c${1:-32};echo;)"
        ```

## Define policy configuration

!!! note "Information about the default policy rules used"

    The default RabbitMQ policy sets quorum queues target group size to 3 for
    the `cyborg` vhost. This can be changed in `base-kustomize/cyborg/base/policies.yaml`.

    ??? example "Default RabbitMQ policy"

        ``` yaml
        apiVersion: rabbitmq.com/v1beta1
        kind: Policy
        metadata:
          name: cyborg-quorum-three-replicas
          namespace: openstack
        spec:
          name: cyborg-quorum-three-replicas
          vhost: "cyborg"
          pattern: ".*"
          applyTo: queues
          definition:
            target-group-size: 3
          priority: 0
          rabbitmqClusterReference:
            name: rabbitmq
        ```

## Run the package deployment

!!! example "Run the Cyborg deployment Script `/opt/genestack/bin/install-cyborg.sh`"

    ``` shell
    --8<-- "bin/install-cyborg.sh"
    ```

!!! tip

    You may need to provide custom values to configure your OpenStack services,
    including Cyborg. Additional information on override options can be found in
    the [OpenStack Helm documentation](https://docs.openstack.org/openstack-helm/latest/).
