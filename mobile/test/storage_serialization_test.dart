import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/services/storage.dart';

void main() {
  late Directory directory;
  setUp(() async => directory = await Directory.systemTemp.createTemp('tailsplit-json-store'));
  tearDown(() => directory.delete(recursive: true));

  test('concurrent replacements persist the last submitted value', () async {
    final file = File('${directory.path}/settings.json');
    await Future.wait([
      for (var i = 0; i < 50; i++) JsonStore.write(File(file.path), {'generation': i}),
    ]);
    expect(await JsonStore.read(file), {'generation': 49});
    expect(await File('${file.path}.tmp').exists(), isFalse);
  });

  test('deletion waits for pending writes and cannot resurrect a store', () async {
    final file = File('${directory.path}/tailnets.json');
    final writes = [for (var i = 0; i < 20; i++) JsonStore.write(file, {'generation': i})];
    final deletion = JsonStore.delete(File(file.path));
    await Future.wait([...writes, deletion]);
    expect(await file.exists(), isFalse);
  });

  test('queued writes snapshot values at submission', () async {
    final file = File('${directory.path}/settings.json');
    final value = <String, Object?>{'generation': 1};
    final save = JsonStore.write(file, value);
    value['generation'] = 2;
    await save;
    expect(await JsonStore.read(file), {'generation': 1});
  });

  test('an unsuccessful write does not block later replacements', () async {
    final file = File('${directory.path}/settings.json');
    await Directory('${file.path}.tmp').create();
    await expectLater(JsonStore.write(file, {'generation': 1}), throwsA(isA<FileSystemException>()));
    await Directory('${file.path}.tmp').delete();
    await JsonStore.write(file, {'generation': 2});
    expect(await JsonStore.read(file), {'generation': 2});
  });
}
