class Recall < Formula
  desc "Find, bookmark, and reopen local Codex and Claude conversations"
  homepage "https://github.com/jborlum/recall"
  url "https://github.com/jborlum/recall/archive/refs/tags/v0.15.0.tar.gz"
  sha256 "c99c67ba2a85e87fe8af804d45f340da9fb42e3eaa25abbe65f13bcc81b2e371"
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
