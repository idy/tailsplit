import 'package:flutter/widgets.dart';

import 'src/app.dart';
import 'src/services/storage.dart';
import 'src/state/app_controller.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final controller = AppController(paths: await AppPaths.resolve());
  runApp(TailSplitApp(controller: controller));
  await controller.load();
}
