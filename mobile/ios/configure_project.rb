#!/usr/bin/env ruby
# Adds TailSplit's Packet Tunnel extension target and App Group entitlements.
require 'xcodeproj'
require 'json'

release = JSON.parse(File.read(File.join(__dir__, '..', 'release-identity.json')))
raise 'Mobile releases must use the Termitoc organization' unless release.fetch('team_id') == 'BGL4WMWD3W'
channel = ARGV.fetch(0, 'app-store')
raise 'Usage: configure_project.rb [beta|app-store]' unless ARGV.length <= 1 && %w[beta app-store].include?(channel)
identity = release.fetch('channels').fetch(channel).merge('team_id' => release.fetch('team_id'))

project = Xcodeproj::Project.open(File.join(__dir__, 'Runner.xcodeproj'))
runner = project.targets.find { |target| target.name == 'Runner' }
raise 'Runner target is missing' unless runner

# The generated Flutter template enables SwiftPM. TailSplit uses CocoaPods
# because its extension must select a Flutter-free PacketTunnel subspec.
project.objects.to_a.each do |object|
  if object.isa == 'PBXBuildFile' && object.display_name.include?('FlutterGeneratedPluginSwiftPackage')
    object.remove_from_project
  elsif object.isa == 'XCSwiftPackageProductDependency' && object.product_name == 'FlutterGeneratedPluginSwiftPackage'
    object.remove_from_project
  elsif object.isa == 'XCLocalSwiftPackageReference' && object.relative_path.include?('FlutterGeneratedPluginSwiftPackage')
    object.remove_from_project
  elsif object.isa == 'PBXFileReference' && object.path.to_s.include?('FlutterGeneratedPluginSwiftPackage')
    object.remove_from_project
  end
end

extension = project.targets.find { |target| target.name == 'PacketTunnel' }
extension ||= project.new_target(:app_extension, 'PacketTunnel', :ios, '15.0')
group = project.main_group.groups.find { |item| item.path == 'PacketTunnel' }
group ||= project.main_group.new_group('PacketTunnel', 'PacketTunnel')
['PacketTunnelProvider.swift', 'Info.plist', 'PacketTunnel.entitlements'].each do |name|
  reference = group.files.find { |item| item.path == name } || group.new_file(name)
  if name.end_with?('.swift') && !extension.source_build_phase.files_references.include?(reference)
    extension.source_build_phase.add_file_reference(reference)
  end
end

project.targets.each do |target|
  target.build_configurations.each do |configuration|
    configuration.build_settings['DEVELOPMENT_TEAM'] = identity.fetch('team_id')
    configuration.build_settings['IPHONEOS_DEPLOYMENT_TARGET'] = '15.0'
  end
end
[runner, extension].each do |target|
  target.build_configurations.each do |configuration|
    configuration.build_settings['TAILSPLIT_APP_GROUP'] = identity.fetch('app_group')
    configuration.build_settings['TAILSPLIT_DISPLAY_NAME'] = identity.fetch('name')
  end
end
runner.build_configurations.each do |configuration|
  configuration.build_settings['CODE_SIGN_ENTITLEMENTS'] = 'Runner/Runner.entitlements'
  configuration.build_settings['PRODUCT_BUNDLE_IDENTIFIER'] = identity.fetch('bundle_id')
end
project.targets.select { |target| target.name == 'RunnerTests' }.each do |target|
  target.build_configurations.each do |configuration|
    configuration.build_settings['PRODUCT_BUNDLE_IDENTIFIER'] = "#{identity.fetch('bundle_id')}.RunnerTests"
  end
end
extension.build_configurations.each do |configuration|
  configuration.build_settings.merge!({
    'APPLICATION_EXTENSION_API_ONLY' => 'YES',
    'CODE_SIGN_ENTITLEMENTS' => 'PacketTunnel/PacketTunnel.entitlements',
    'GENERATE_INFOPLIST_FILE' => 'NO',
    'INFOPLIST_FILE' => 'PacketTunnel/Info.plist',
    'PRODUCT_BUNDLE_IDENTIFIER' => identity.fetch('packet_tunnel_bundle_id'),
    'PRODUCT_NAME' => '$(TARGET_NAME)',
    'PRODUCT_MODULE_NAME' => 'PacketTunnel',
    'SWIFT_VERSION' => '5.0',
    'CLANG_ENABLE_MODULES' => 'YES',
    'SKIP_INSTALL' => 'YES',
    'TARGETED_DEVICE_FAMILY' => '1,2',
  })
end
{ 'Debug' => 'Debug.xcconfig', 'Profile' => 'Profile.xcconfig', 'Release' => 'Release.xcconfig' }.each do |name, file|
  reference = group.files.find { |item| item.path == file } || group.new_file(file)
  extension.build_configurations.find { |configuration| configuration.name == name }.base_configuration_reference = reference
end
unless runner.dependencies.any? { |dependency| dependency.target == extension }
  runner.add_dependency(extension)
end
embed = runner.copy_files_build_phases.find { |phase| phase.name == 'Embed App Extensions' }
embed ||= runner.new_copy_files_build_phase('Embed App Extensions')
embed.dst_subfolder_spec = '13'
unless embed.files_references.include?(extension.product_reference)
  file = embed.add_file_reference(extension.product_reference)
  file.settings = { 'ATTRIBUTES' => ['RemoveHeadersOnCopy'] }
end
# Embed extensions before Flutter's thinning phase to avoid an Xcode cycle via
# the application Info.plist and PlugIns directory.
runner.build_phases.delete(embed)
thin_index = runner.build_phases.index { |phase| phase.display_name == 'Thin Binary' }
runner.build_phases.insert(thin_index || runner.build_phases.length, embed)
project.save
puts "Configured #{identity.fetch('name')} (#{channel}) and embedded PacketTunnel."
