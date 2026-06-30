#!/bin/sh

sudo docker build \
    -f kcloud-loci/Dockerfile.base \
    --build-arg FROM=ubuntu:jammy \
    --build-arg CEPH_REPO='deb https://download.ceph.com/debian-reef/ jammy main' \
    --tag ghcr.io/openkcloud/kcloud-base:ubuntu_jammy \
    --progress=plain \
    kcloud-loci

rm -rf kcloud-loci/data/requirements
cp -a kcloud-requirements kcloud-loci/data/requirements

sudo docker build \
    -f kcloud-loci/Dockerfile \
    --target requirements \
    --build-arg FROM=ghcr.io/openkcloud/kcloud-base:ubuntu_jammy \
    --build-arg PROJECT=requirements \
    --build-arg PROJECT_REPO=https://github.com/openkcloud/kcloud-requirements \
    --build-arg PROJECT_REF=kcloud/2024.1 \
    --tag ghcr.io/openkcloud/kcloud-requirements:kcloud-2024.1-ubuntu_jammy \
    --progress=plain \
    kcloud-loci

rm -rf kcloud-loci/data/nova/
cp -a kcloud-nova kcloud-loci/data/nova

sudo docker build \
    -f kcloud-loci/Dockerfile \
    --build-arg FROM=ghcr.io/openkcloud/kcloud-nova_base:ubuntu_jammy \
    --build-arg WHEELS=ghcr.io/openkcloud/kcloud-requirements:kcloud-2024.1-ubuntu_jammy \
    --build-arg PROJECT=nova \
    --build-arg PROJECT_REPO=https://github.com/openkcloud/kcloud-nova \
    --build-arg PROJECT_REF=stable/2024.1 \
    --build-arg PROFILES='fluent ceph linuxbridge openvswitch configdrive qemu apache migration' \
    --build-arg DIST_PACKAGES='net-tools openssh-server' \
    --tag ghcr.io/openkcloud/kcloud-nova:stable-2024.1 \
    kcloud-loci

sudo docker save ghcr.io/openkcloud/kcloud-nova:stable-2024.1 -o /tmp/kcloud-nova_stable-2024.1.tar
sudo chown kcloud.kcloud /tmp/kcloud-nova_stable-2024.1.tar


#
# in worker nodes
#

# sudo ctr -n k8s.io images rm ghcr.io/openkcloud/kcloud-nova:stable-2024.1
# scp kcloud@<build server>:/tmp/kcloud-nova_stable-2024.1.tar /tmp/
# sudo ctr -n k8s.io images import /tmp/kcloud-nova_stable-2024.1.tar

