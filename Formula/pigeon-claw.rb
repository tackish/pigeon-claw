class PigeonClaw < Formula
  desc "Discord-based remote Mac agent - lightweight alternative to openclaw"
  homepage "https://github.com/tackish/pigeon-claw"
  version "0.0.39"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/tackish/pigeon-claw/releases/download/v#{version}/pigeon-claw-darwin-arm64.tar.gz"
      sha256 "a7a3ea767b7d63567f14e3d45a2f3b78a333ce383c27427bcb21cb5f251733e2"
    else
      url "https://github.com/tackish/pigeon-claw/releases/download/v#{version}/pigeon-claw-darwin-amd64.tar.gz"
      sha256 "5692110265c5b1dd834ffd7716cff54bab43c42094f50f3c9ee10779e8b39248"
    end
  end

  def install
    bin.install "pigeon-claw"
  end

  def post_install
    (var/"log/pigeon-claw").mkpath
    (etc/"pigeon-claw").mkpath
  end

  def caveats
    <<~EOS
      Quick Start:
        1. pigeon-claw permission     # Claude CLI + macOS 권한 설정
        2. cp #{etc}/pigeon-claw/sample_env ~/.pigeon-claw/.env
        3. pigeon-claw start           # 백그라운드 시작

      Config: ~/.pigeon-claw/.env
      Logs:   pigeon-claw logs
    EOS
  end

  service do
    run [opt_bin/"pigeon-claw", "serve"]
    working_dir var/"pigeon-claw"
    log_path var/"log/pigeon-claw/stdout.log"
    error_log_path var/"log/pigeon-claw/stderr.log"
    keep_alive true
    environment_variables HOME: Dir.home, PATH: std_service_path_env
  end

  test do
    assert_match "pigeon-claw", shell_output("#{bin}/pigeon-claw version")
  end
end
