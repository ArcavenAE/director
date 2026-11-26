#!/usr/bin/env bash
# Fixture: the case table a wrapper keeps of its own. Read by verify-cast-roles.sh
# through --table; never run.
case "$MARVEL_ROLE" in
  scoped-builder) WROLE=builder;;
  *)              WROLE="$MARVEL_ROLE";;
esac
