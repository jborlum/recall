class Recall < Formula
  desc "Find, bookmark, and reopen local Codex and Claude conversations"
  homepage "https://github.com/jborlum/recall"
  url "https://github.com/jborlum/recall/archive/refs/tags/v0.15.1.tar.gz"
  sha256 "c8ae3a738e93a3cfc9de27f83206e2ff11246565db3e2fd15e7fa06ac67bedc2"
  head "https://github.com/jborlum/recall.git", branch: "main"

  depends_on "go" => :build
  depends_on "fzf"

  def install
    # std_go_args already supplies -trimpath, -o, and -s -w.
    system "go", "build", *std_go_args(ldflags: "-X main.version=#{version}"), "."
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/recall --version")
  end
end
