#!/bin/bash
# pip install wrapper invoked by tox install_command.
#
# Why this wrapper exists:
# Old sdist-only XStatic packages bundled with Horizon (notably
# XStatic-Angular-Schema-Form-0.8.13.0) still call
# pkg_resources.declare_namespace from setup.py at build time.
# pkg_resources was dropped from the default setuptools wheel in
# setuptools 81, so any PEP 517 isolated build env that installs the
# latest setuptools blows up with ModuleNotFoundError: No module
# named 'pkg_resources'.
#
# Constraining pip's isolated build env via PIP_CONSTRAINT does not
# reliably propagate (pypa/pip#11953 and related). Instead we:
#   1) Pre-install setuptools<81 (and wheel) into the testenv venv.
#   2) Run the actual install with --no-build-isolation so any sdist
#      build reuses the venv's setuptools<81 directly.
set -eu

python -I -m pip install --upgrade --quiet 'setuptools<81' wheel
exec python -I -m pip install --no-build-isolation "$@"
