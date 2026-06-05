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
  version "1.4.0" # bumped per release

  on_macos do
    on_arm do
      url "https://github.com/joshlopes/minimalist-cost-tracker/releases/download/v#{version}/cost-tracker_darwin_arm64.tar.gz"
      sha256 "2798a1d01c924a2e279604d5ff8e93d282b2d8a7e9730dbe723b8262d6e18ea9"
    end
    on_intel do
      url "https://github.com/joshlopes/minimalist-cost-tracker/releases/download/v#{version}/cost-tracker_darwin_amd64.tar.gz"
      sha256 "f3515d42c903a7a763ac3d1c83460b1b2a8c07fb2497561fae0f43e9dbb5dc0c"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/joshlopes/minimalist-cost-tracker/releases/download/v#{version}/cost-tracker_linux_arm64.tar.gz"
      sha256 "c7fe00dda49fc25adafc16bf37d24b8290a773a85d91eb0ddd301f2ea28140e4"
    end
    on_intel do
      url "https://github.com/joshlopes/minimalist-cost-tracker/releases/download/v#{version}/cost-tracker_linux_amd64.tar.gz"
      sha256 "330a80d251ef3d7ee2d54a8b84834d56333a99dcbee7bb50b52543ded7078291"
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
