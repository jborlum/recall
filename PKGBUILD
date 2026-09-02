pkgname=recall
pkgver=0.3.0
pkgrel=1
pkgdesc="Find, bookmark, and reopen local Codex and Claude conversations"
arch=('x86_64' 'aarch64')
url="https://github.com/jborlum/recall"
license=('LicenseRef-All-Rights-Reserved')
makedepends=('git' 'go')
optdepends=('fzf: interactive fuzzy picker')
options=('!debug')
source=("${pkgname}-src::git+ssh://git@github.com/jborlum/recall.git#tag=v${pkgver}")
sha256sums=('SKIP')

build() {
  cd "$srcdir/$pkgname-src"
  export CGO_ENABLED=0
  export GOCACHE="$srcdir/go-build-cache"
  go build \
    -buildmode=pie \
    -trimpath \
    -ldflags="-s -w -X main.version=$pkgver" \
    -o recall .
}

check() {
  cd "$srcdir/$pkgname-src"
  export GOCACHE="$srcdir/go-build-cache"
  go test ./...
}

package() {
  cd "$srcdir/$pkgname-src"
  install -Dm755 recall "$pkgdir/usr/bin/recall"
  install -Dm644 README.md "$pkgdir/usr/share/doc/$pkgname/README.md"
}
