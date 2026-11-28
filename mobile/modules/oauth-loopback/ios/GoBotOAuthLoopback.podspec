Pod::Spec.new do |s|
  s.name = 'GoBotOAuthLoopback'
  s.version = '1.0.0'
  s.summary = 'Transient loopback OAuth receiver'
  s.description = s.summary
  s.license = 'MIT'
  s.author = 'Go Bot contributors'
  s.homepage = 'https://github.com/xyc999-ai/go-bot'
  s.platforms = { :ios => '16.4' }
  s.source = { :git => s.homepage }
  s.static_framework = true
  s.swift_version = '5.9'
  s.dependency 'ExpoModulesCore'
  s.frameworks = 'Network', 'SafariServices'
  s.source_files = '**/*.swift'
end
