#!/bin/sh
# Downloads the Linux x86_64 / cp310 wheels for Playwright and its
# dependencies into ./pip-wheels/. The Docker build copies these wheels
# into the image and pip-installs them offline, so we never depend
# on PyPI being reachable from Fly's remote builder (which has been
# unreliable: connection-reset-by-peer 5/5 retries).
#
# (Not named `vendor/` because Go modules reserves that path and
# breaks `go build` when it appears next to go.mod.)
#
# Re-run after bumping the Playwright version in the Dockerfile.
set -eu

cd "$(dirname "$0")/.."
mkdir -p pip-wheels
rm -f pip-wheels/*.whl

PLATFORM_FLAGS="--platform manylinux1_x86_64 --platform manylinux_2_17_x86_64 --platform manylinux2014_x86_64 --abi cp310 --python-version 310 --only-binary=:all: --no-deps"

pip3 download $PLATFORM_FLAGS --dest pip-wheels "playwright==1.49.1"
pip3 download $PLATFORM_FLAGS --dest pip-wheels "greenlet==3.1.1"
pip3 download --no-deps --only-binary=:all: --dest pip-wheels "pyee==12.0.0"
pip3 download --no-deps --only-binary=:all: --dest pip-wheels "typing-extensions"

echo "pip-wheels/ contents:"
ls -la pip-wheels/
