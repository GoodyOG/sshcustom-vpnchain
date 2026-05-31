# third_party patch tracking

SSHCustom-Magisk vendors selected packages from upstream `golang.org/x/crypto`
under [`third_party/golang.org/x/crypto/`](golang.org/x/crypto/) and replaces
the upstream import path through `go.mod`:

```go
replace golang.org/x/crypto => ./third_party/golang.org/x/crypto
```

The vendor copy is locked in to keep builds reproducible across Go releases,
and to give us the option of carrying compatibility fixes for Dropbear-style
SSH servers that mainline x/crypto sometimes regresses on.

## Known deltas vs upstream

As of v2.0.0 we have **not catalogued** the exact diff between this tree and a
specific upstream tag. Patches in earlier development referenced "Dropbear
compatibility and larger receive tolerance," but those changes were never
isolated into a patch series and the original upstream baseline was never
recorded.

If you are about to apply a CVE fix, refresh upstream, or audit our vendor
tree, follow this procedure to discover the actual delta:

```bash
# 1. From the repo root, pick a candidate upstream tag. v0.31.0 was the
#    closest tag at the time of vendoring; newer tags are fine if you also
#    update go.sum and re-test the SSH handshake.
git clone https://go.googlesource.com/crypto /tmp/xcrypto-upstream
cd /tmp/xcrypto-upstream && git checkout v0.31.0 && cd -

# 2. Diff each vendored subpackage against upstream.
for pkg in ssh chacha20 curve25519 internal/poly1305 internal/alias blowfish; do
  echo "===== $pkg ====="
  diff -ruN /tmp/xcrypto-upstream/$pkg third_party/golang.org/x/crypto/$pkg \
    | head -200
done
```

Save the output of that diff into this file under a dated section and you have
a real patch series.

## Refresh procedure

1. Decide the target upstream tag.
2. Reapply (or drop) any deltas captured here.
3. Update `third_party/golang.org/x/crypto/go.mod` so its `go` directive does
   not exceed our root `go.mod`.
4. Run `go test ./...` and rebuild a real device install. Verify in particular:
   - SSH handshakes against Dropbear servers (the most common SSH+Payload
     server software).
   - Chacha20-Poly1305 transport, since older Dropbear versions only support
     this cipher.
   - Long-lived connections under transparent TCP load (regressions in the
     packet-size handling have shown up here historically).

## Why we vendor at all

- **Reproducibility.** A `go.sum` pin keeps us safe from upstream surprises.
  Vendoring goes a step further and survives the loss of `go.sum` integrity.
- **Optional carries.** When upstream introduces a compatibility regression
  for legacy SSH servers, we can hold back without rolling back our own Go
  version.
- **Auditability.** Every byte that ends up in the daemon binary is in this
  repository. There is nothing to fetch from the internet at build time.
