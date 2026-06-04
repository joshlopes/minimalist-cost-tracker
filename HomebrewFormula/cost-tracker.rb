# Homebrew formula for the Claude Code cost-tracker.
#
# Install directly from this repo (no separate tap needed):
#
#   brew install joshlopes/minimalist-cost-tracker/cost-tracker
#
# or tap first:
#
#   brew tap joshlopes/minimalist-cost-tracker https://github.com/joshlopes/minimalist-cost-tracker
#   brew install cost-tracker
#
# The version/url/sha256 lines below are bumped per release — see
# "Releasing" in README.md. `brew bump-formula-pr` (or a release-workflow step)
# can automate it.
class CostTracker < Formula
  desc "Track Claude Code session token usage and cost in a local dashboard"
  homepage "https://github.com/joshlopes/minimalist-cost-tracker"
  version "1.0.0" # bumped per release

  on_macos do
    on_arm do
      url "https://github.com/joshlopes/minimalist-cost-tracker/releases/download/v#{version}/cost-tracker_darwin_arm64.tar.gz"
      sha256 "4f1ba19e1eb7e0670674a7ee907f960fc36715ddf7d9018b72b580fc9f452d83"
    end
    on_intel do
      url "https://github.com/joshlopes/minimalist-cost-tracker/releases/download/v#{version}/cost-tracker_darwin_amd64.tar.gz"
      sha256 "ee6fee0a2ab748006e0ad7d3e447b48649a39c6f2b5a77ea9766c1f9af50bb81"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/joshlopes/minimalist-cost-tracker/releases/download/v#{version}/cost-tracker_linux_arm64.tar.gz"
      sha256 "49c804f1f51c8a85c62f194b7e2dd71f509637c6385177f166ffa071365877e4"
    end
    on_intel do
      url "https://github.com/joshlopes/minimalist-cost-tracker/releases/download/v#{version}/cost-tracker_linux_amd64.tar.gz"
      sha256 "f11572d9a3632febb6c77b971f73d267a22ffcf2e5365daa887f04a247a51fc6"
    end
  end

  def install
    bin.install "cost-tracker"
  end

  def caveats
    <<~EOS
      Wire the Claude Code hooks (once) and start the dashboard:

        cost-tracker install-hooks
        cost-tracker service install   # runs on login at http://localhost:7842

      Hooks take effect on your next Claude Code session.
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/cost-tracker version")
  end
end
