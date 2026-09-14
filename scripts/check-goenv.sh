#!/bin/sh
# Preflight for building pdx with the private Nexen module (spec §3.1).
ok=1
case ",$(go env GOPRIVATE)," in *,lab.protype.tw,*|*lab.protype.tw*) ;; *)
  echo 'missing: go env -w GOPRIVATE=lab.protype.tw'; ok=0;; esac
if ! git config --global --get-all 'url.ssh://git@lab.protype.tw:9079/.insteadOf' 2>/dev/null | grep -qx 'https://lab.protype.tw/'; then
  echo 'missing: git config --global url."ssh://git@lab.protype.tw:9079/".insteadOf "https://lab.protype.tw/"'; ok=0
fi
[ "$ok" = 1 ]
