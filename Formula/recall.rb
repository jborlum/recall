class Recall < Formula
  desc "Find, bookmark, and reopen local Codex and Claude conversations"
  homepage "https://github.com/jborlum/recall"
  url "https://github.com/jborlum/recall/archive/refs/tags/v0.14.0.tar.gz"
  sha256 "9fae60a66b097c00fed0c070805c3c2430ed6337fc9a91d3c521e2ddafee4229"
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
