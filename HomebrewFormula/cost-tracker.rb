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
  version "1.5.0" # bumped per release

  on_macos do
    on_arm do
      url "https://github.com/joshlopes/minimalist-cost-tracker/releases/download/v#{version}/cost-tracker_darwin_arm64.tar.gz"
      sha256 "c33229c26a1d5f9070f5590564e9207758bbf4ff299ff440e70da904f7a2a8e3"
    end
    on_intel do
      url "https://github.com/joshlopes/minimalist-cost-tracker/releases/download/v#{version}/cost-tracker_darwin_amd64.tar.gz"
      sha256 "51995b5afd5ce4e804236db230531ff61cf51a243b73711ad2e540341ff4369b"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/joshlopes/minimalist-cost-tracker/releases/download/v#{version}/cost-tracker_linux_arm64.tar.gz"
      sha256 "1725d9dc126b82799c3ac2da1ff8ba2fa7d5c244d8846c03d9dc41467680f74c"
    end
    on_intel do
      url "https://github.com/joshlopes/minimalist-cost-tracker/releases/download/v#{version}/cost-tracker_linux_amd64.tar.gz"
      sha256 "9d1e0fffab8e9a1b5e75cd5ab3867bad878f1f5f5e98f09968e6f2ec211331c7"
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
