class IosScreencast < Formula
  desc "Low-latency H.264 screen streaming for iOS devices, including system alerts"
  homepage "https://github.com/hakanor/ios-screencast"
  version "0.1.0"
  url "https://github.com/hakanor/ios-screencast/releases/download/v#{version}/ios-screencast-v#{version}-macos.tar.gz"
  sha256 "REPLACE_WITH_SHA256"
  license "MIT"

  depends_on :macos

  resource "ios-device-frames" do
    url "https://github.com/hakanor/ios-device-frames/releases/download/v0.1.0/ios-device-frames-v0.1.0-macos.tar.gz"
    sha256 "REPLACE_WITH_SHA256"
  end

  def install
    bin.install "ios-screencast"
    resource("ios-device-frames").stage { bin.install "ios-device-frames" }
  end

  def caveats
    <<~EOS
      Sign and install both IPAs from the release once, with your own signing tool:
        https://github.com/hakanor/ios-screencast/releases/tag/v#{version}
          ios-screencast.ipa
          ios-screencast-runner.ipa

      Then, with the phone connected and unlocked:
        ios-screencast start
    EOS
  end

  test do
    assert_match "usage: ios-screencast", shell_output("#{bin}/ios-screencast --help")
  end
end
