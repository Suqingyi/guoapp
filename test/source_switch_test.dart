import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  // 复现「连点两次切换后站源消失」：可见集合不变时，任意切换序列都应保持在可见源上。
  test('repeated source switches stay within visible sources', () {
    final visible = SourceSite.visibleValues(unlocked: true);
    final allowed = visible.map((s) => s.id).toSet();

    // 模拟用户连续切换：红果 -> 黄果 -> 黄豆 -> 野果 -> 红果
    final sequence = ['huangguo-video', 'huangdou', 'yeguo', 'hongguo'];
    var current = visible.first;

    for (final next in sequence) {
      final target = SourceSite.byId(next);
      // byId 必须解析到真实存在的站源，不能静默回落到红果
      expect(SourceSite.isKnown(target.id), isTrue,
          reason: '$next 应解析为已知站源，实得 ${target.id}');
      if (allowed.contains(target.id)) {
        current = target;
      }
      // 每一步之后当前源都必须仍可见
      expect(allowed.contains(current.id), isTrue,
          reason: '切换 $next 后当前源 ${current.id} 应仍可见');
      expect(allowed, contains(current.id));
    }
  });

  test('byId never silently degrades a yellow-fruit group name to hongguo', () {
    for (final group in ['huangguo']) {
      final site = SourceSite.byId(group);
      expect(site.id, isNot('hongguo'),
          reason: '分组名 $group 不应回落到红果');
      expect(site.groupId, group);
    }
    // 黄果三个入口各自可解析
    for (final id in ['huangguo-video', 'huangguoai', 'cloudfront']) {
      expect(SourceSite.byId(id).id, id);
      expect(SourceSite.byId(id).groupId, 'huangguo');
    }
  });

  test('visible set is stable across repeated lookups', () {
    final a = SourceSite.visibleValues(unlocked: true).map((s) => s.id).toList();
    final b = SourceSite.visibleValues(unlocked: true).map((s) => s.id).toList();
    expect(a, b, reason: '可见集合多次读取应完全一致');
  });
}
