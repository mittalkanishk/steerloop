#!/usr/bin/env bash
set -eu

branch=$(git rev-parse --abbrev-ref HEAD)
if [ "$branch" != "main" ]; then
  echo "Releases are tagged from main; you are on $branch." >&2
  exit 1
fi
if [ -n "$(git status --porcelain)" ]; then
  echo "The working tree has uncommitted changes." >&2
  exit 1
fi
git fetch --quiet --tags origin main
if [ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]; then
  echo "Local main and origin/main differ; pull or push first." >&2
  exit 1
fi
latest_tag=$(git tag -l 'v*' --sort=-v:refname | head -n 1)
echo "Latest tag is ${latest_tag:-none}"
new_tag=${VERSION:-}
if [ -z "$new_tag" ]; then
  printf "Enter new release tag (with a v, e.g. v1.2.3): "
  read -r new_tag
fi
if ! printf '%s' "$new_tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "'$new_tag' is not of the form vX.Y.Z." >&2
  exit 1
fi
if [ -n "$(git tag -l "$new_tag")" ]; then
  echo "Tag $new_tag already exists." >&2
  exit 1
fi
git tag -a "$new_tag" -m "$new_tag"
git push origin "$new_tag"
echo "Released new tag $new_tag"
