================
Magnum CAPI Helm
================

.. image:: https://governance.openstack.org/tc/badges/magnum-capi-helm.svg

OpenStack Magnum driver using Helm to create K8s clusters
with Cluster API.

The driver uses `capi-helm-charts <https://github.com/azimuth-cloud/capi-helm-charts>`_
to create the K8s resources needed to provision a K8s cluster using
Cluster API, including various useful add-ons like a CNI and a monitoring
stack.

Note, the above Helm charts are intended to be
a way to share a reference method to create K8s
on OpenStack. The charts are not expected or
intended to be specific to Magnum. The hope is
they can also be used by ArgoCD, Flux or Azimuth
to create K8s clusters on OpenStack.

Work on this driver started upstream around October 2021.
After failing to get merged during Bobcat,
we created this downstream repo as a stop-gap to help
those wanting to use this driver now.
https://specs.openstack.org/openstack/magnum-specs/specs/bobcat/clusterapi-driver.html

* Free software: Apache license
* Documentation: https://docs.openstack.org/magnum-capi-helm/latest
* Source: https://opendev.org/openstack/magnum-capi-helm
* Bugs: https://bugs.launchpad.net/magnum

kcloud additions
================

The ``kcloud`` branch of this fork adds the following to the upstream driver.

Cluster labels
--------------

These labels are read in the same way as the upstream ones. A cluster label
overrides the cluster template label, and the template label overrides the
default in the ``[capi_helm_cluster_labels]`` section of ``magnum.conf``.
Magnum does not allow labels to be changed after a cluster is created.

``nvidia_gpu_operator_enabled`` (default ``false``)
  Installs the NVIDIA GPU operator addon of the chart. The chart turns it on by
  default, but kcloud-operator manages GPUs in this deployment, so the driver
  turns it off unless the label is set.

``kube_dashboard_enabled`` (default ``false``)
  Installs the Kubernetes Dashboard addon. Upstream turns it on by default;
  this fork turns it off unless the label is set. An existing cluster without
  the label loses the dashboard the next time the driver updates its Helm
  release.

``mellanox_network_operator_enabled`` (default ``false``)
  Installs the Mellanox network operator addon of the chart. The chart turns it
  on by default, but it has nothing to manage without Mellanox hardware, so the
  driver turns it off unless the label is set.

``node_feature_discovery_enabled`` (default ``false``)
  Installs the node feature discovery addon of the chart. The chart turns it on
  by default, but kcloud-operator labels nodes itself and does not use the
  labels it publishes, so the driver turns it off unless the label is set. The
  chart still installs it when the NVIDIA GPU operator or the Mellanox network
  operator is enabled, because both depend on it. An existing cluster without
  the label loses it the next time the driver updates its Helm release, unless
  one of those operators is enabled.

``kcloud_operator_enabled`` (default ``true``)
  Installs `kcloud-operator <https://github.com/openkcloud/kcloud-operator>`_,
  which manages NPU and GPU accelerators, as a custom addon. kcloud-operator
  deploys the NVIDIA device plugin itself, so it cannot run together with the
  NVIDIA GPU operator:

  * If ``kcloud_operator_enabled=true`` is set explicitly, kcloud-operator is
    installed and the NVIDIA GPU operator is not, even if
    ``nvidia_gpu_operator_enabled=true`` is also set.
  * If only ``nvidia_gpu_operator_enabled=true`` is set explicitly, the NVIDIA
    GPU operator is installed and kcloud-operator is not.
  * If neither is set, the defaults apply: kcloud-operator is installed and
    the NVIDIA GPU operator is not. If the site default for kcloud-operator is
    changed to ``false``, the NVIDIA GPU operator default applies.
  * Both can be set to ``false``, for example on clusters without
    accelerators.

The defaults are read again whenever the driver updates the Helm release:
on resize, upgrade, and when nodegroups are created, updated or deleted. An
existing cluster that set neither label switches to the new defaults on its
next such operation.

For example, to use the NVIDIA GPU operator instead of kcloud-operator::

  openstack coe cluster create my-cluster \
    --cluster-template my-template \
    --labels nvidia_gpu_operator_enabled=true

Node group images
-----------------

A worker node group created with ``--image`` boots from that image instead
of the cluster template image, so accelerator nodes can use an image that
carries their drivers while other nodes keep the template image::

  openstack coe nodegroup create my-cluster npu-workers \
    --flavor m1.large.npu --image ubuntu-jammy-kube-v1.36.4-npu

The image must have the same ``kube_version`` and ``os_distro`` properties as
the cluster template image, because the Kubernetes version and OS are set for
the whole cluster; the driver rejects the node group otherwise. It needs
capi-helm-charts 0.25.0 or later, which read ``nodeGroups[].machineImageId``.

The default node groups, and node groups created without ``--image``, use the
template image, as before. Magnum copies the template image into those node
groups; on a cluster upgrade the driver moves them to the new template image.

A node group with its own image keeps it on upgrade. The driver checks it
against the new template image first, and rejects the upgrade if the
``kube_version`` or ``os_distro`` no longer matches: the cluster stays on its
template and shows ``UPDATE_FAILED`` with the reason. Delete that node group,
upgrade, and create it again with an image that matches.

Configuration options
---------------------

These options are in the ``[capi_helm]`` section of ``magnum.conf``.

``kcloud_operator_chart_repo``
  The Helm repository for the kcloud-operator chart, by default
  ``https://openkcloud.github.io/kcloud-operator``. It must be an http(s)
  repository, because cluster-api-addon-provider does not accept ``oci://``
  references.

``kcloud_operator_chart_version`` (default ``0.8.1``)
  The kcloud-operator chart version.

``kcloud_operator_values``
  The Helm values for the chart, as one line of YAML or JSON. The default is
  the Kubernetes 1.31-1.34 preset from kcloud-operator's
  ``deploy/integration/release.yaml``. Setting this option replaces the whole
  preset; it is not merged with the default, so copy the preset and change it.

  The chart validates its values against a schema, so the chart version and
  the preset must come from the same ``release.yaml`` profile. Change them
  together. Guest clusters outside the 1.31-1.34 range need the other profile.

  The value must be on one line. openstack-helm writes it into
  ``magnum.conf`` without continuation indent, so a multi-line value breaks the
  file.

``kcloud_operator_namespace`` (default ``kcloud``)
  The namespace of the kcloud-operator release on the guest cluster.

``kcloud_operator_registry`` (default ``ghcr.io/openkcloud``)
  The registry for the images kcloud builds. It is set as ``global.registry``
  on top of ``kcloud_operator_values``.

``kcloud_operator_vendor_registry`` (default empty)
  The registry for third-party vendor images, set as ``global.vendorRegistry``.
  When it is empty, the images are pulled from their upstream registries. For
  an air-gapped site, set it to a mirror that keeps the
  ``<mirror>/<org>/<image>`` layout.

``registry_mirrors`` (default empty)
  Registry mirrors for containerd on the guest cluster, as
  ``<registry>:<mirror url>`` pairs, for example
  ``docker.io:https://registry.example.com/v2/docker.io``. The chart defines
  its own mirrors for ``docker.io``, ``ghcr.io``, ``nvcr.io``, ``quay.io`` and
  ``registry.k8s.io``; the driver removes them, so a registry that is not
  listed here is pulled from upstream directly. Leave the option empty to use
  no mirrors.

Container image
---------------

Each push to ``kcloud`` builds ``ghcr.io/openkcloud/kcloud-magnum``. The image
is the Genestack magnum image (``2025.1-latest``) with this driver installed
over it; magnum itself is not rebuilt. It is tagged ``2025.1`` and
``2025.1-<driver version>``. The base tag changes weekly, so pin the image by
digest in production.

Contributing
------------

Changes go to a topic branch and a pull request against ``kcloud``. A
maintainer merges it by commenting ``/approve``.
