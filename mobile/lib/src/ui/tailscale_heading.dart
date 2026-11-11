import 'package:flutter/widgets.dart';

import '../theme/tokens.dart';

/// Identifies the network provider without replacing TailSplit's page title.
class TailscaleHeading extends StatelessWidget {
  const TailscaleHeading({super.key});

  @override
  Widget build(BuildContext context) {
    final color = labelColor(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 4, 20, 10),
      child: Semantics(
        header: true,
        child: Row(
          children: [
            ExcludeSemantics(
              child: CustomPaint(size: const Size.square(24), painter: _TailscaleMark(color)),
            ),
            const SizedBox(width: 10),
            Text('Tailscale', style: TextStyle(fontSize: 17, fontWeight: FontWeight.w600, color: color)),
          ],
        ),
      ),
    );
  }
}

/// Nine-dot geometry and opacity from Tailscale's official mark:
/// https://github.com/tailscale/tailscale/blob/main/client/web/src/assets/icons/tailscale-logo.svg
/// The adjacent word uses the app's native system font.
class _TailscaleMark extends CustomPainter {
  const _TailscaleMark(this.color);
  final Color color;

  @override
  void paint(Canvas canvas, Size size) {
    final solid = Paint()..color = color;
    final muted = Paint()..color = color.withValues(alpha: .2);
    final unit = size.shortestSide / 8;
    for (var row = 0; row < 3; row++) {
      for (var column = 0; column < 3; column++) {
        canvas.drawCircle(
          Offset((1 + 3 * column) * unit, (1 + 3 * row) * unit),
          unit,
          row == 1 || (row == 2 && column == 1) ? solid : muted,
        );
      }
    }
  }

  @override
  bool shouldRepaint(covariant _TailscaleMark oldDelegate) => oldDelegate.color != color;
}
