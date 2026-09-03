class Recall < Formula
  desc "Find, bookmark, and reopen local Codex and Claude conversations"
  homepage "https://github.com/jborlum/recall"
  head "https://github.com/jborlum/recall.git", branch: "main"

  depends_on "go" => :build
  depends_on "fzf"

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=#{version}"), "."
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/recall --version")
  end
end
