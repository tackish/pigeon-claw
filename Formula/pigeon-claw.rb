class PigeonClaw < Formula
  desc "Discord-based remote Mac agent - lightweight alternative to openclaw"
  homepage "https://github.com/tackish/pigeon-claw"
  version "0.0.43"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/tackish/pigeon-claw/releases/download/v#{version}/pigeon-claw-darwin-arm64.tar.gz"
      sha256 "27955f7b2bf1ecaf59c4990a1a578eb1de40255f39678c33d420a367e2c5a8e8"
    else
      url "https://github.com/tackish/pigeon-claw/releases/download/v#{version}/pigeon-claw-darwin-amd64.tar.gz"
      sha256 "e3ca137ddc71e20530b18ca3d7847e9871bd0136338aaa4489c4198933ef36c5"
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
