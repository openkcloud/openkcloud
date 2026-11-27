# syntax = docker/dockerfile:1
#
# Overlay image for the kcloud deployment.
#
# This does NOT rebuild magnum. It starts from the Genestack magnum image the
# deployment already runs and replaces exactly one thing: the magnum-capi-helm
# driver, which is installed from the source in this repository. magnum itself
# (stable/2025.1), its CVE patches and the bundled helm binary are inherited
# from the base unchanged.
#
# The default tracks `2025.1-latest` so magnum bug fixes and library updates
# arrive without editing this file. That tag is rewritten weekly and its content
# is not stable -- upstream builds a {1.2.1, master} matrix of the capi driver
# and both legs publish this same tag, so which driver it carries varies run to
# run. That does not affect us: the driver is replaced below regardless.
#
# Because the tag moves, the workflow resolves it to a digest before building
# and passes it back in through this argument, so every published image records
# exactly which base it was built on. Pin a digest here to freeze the base.

ARG MAGNUM_IMAGE=ghcr.io/rackerlabs/genestack-images/magnum:2025.1-latest

FROM ${MAGNUM_IMAGE}

LABEL org.opencontainers.image.name="kcloud-magnum-capi-helm"
LABEL org.opencontainers.image.description="Genestack magnum image with the kcloud magnum-capi-helm driver"

USER root

# pbr derives the package version from git metadata, and .git is excluded from
# the build context by .dockerignore. Without an explicit version the build
# fails, so the workflow computes one and passes it in.
#
# The value must be parseable by pbr, not merely valid PEP 440: magnum_capi_helm
# calls pbr.version.VersionInfo().version_string() at import time, and pbr's
# parser does int(component[3:]) on the dev segment. A local version segment
# ("1.3.0.dev46+kcloud") therefore raises ValueError on import and magnum fails
# to load the driver at startup. Keep this to <tag>.dev<count>; provenance is
# carried by the revision label below instead.
ARG PLUGIN_VERSION=0.0.0.dev0
ENV PBR_VERSION=${PLUGIN_VERSION}

ARG PLUGIN_COMMIT=unknown
LABEL org.opencontainers.image.revision="${PLUGIN_COMMIT}"

COPY . /src

# --no-deps is deliberate. The base image's dependency set was resolved against
# the 2025.1 upper-constraints; letting pip resolve this package's requirements
# would silently upgrade magnum and oslo libraries and break that pinning.
# A genuinely new runtime dependency has to be added here explicitly, pinned.
RUN /var/lib/openstack/bin/pip install --no-cache-dir --no-deps --force-reinstall /src \
  && rm -rf /src \
  && find /var/lib/openstack -name '*.pyc' -delete \
  && find /var/lib/openstack -name '__pycache__' -type d -prune -exec rm -rf {} +

# Fail the build here rather than at magnum startup. The import is the load
# bearing part: magnum_capi_helm evaluates its pbr version on import, so a
# version string pbr cannot parse only shows up this way -- checking entry-point
# metadata alone passes and the driver still dies at runtime.
RUN /var/lib/openstack/bin/python -c "\
import magnum_capi_helm; \
from importlib.metadata import entry_points, version; \
eps = [e for e in entry_points(group='magnum.drivers') if e.name == 'k8s_capi_helm_v1']; \
assert eps, 'magnum.drivers entry point k8s_capi_helm_v1 is missing'; \
print('magnum-capi-helm', version('magnum-capi-helm'), '->', eps[0].value)"

WORKDIR /var/lib/openstack
USER 42424:42424
ENTRYPOINT ["/var/lib/openstack/bin/magnum-manage"]
