import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('byId resolves a real source id', () {
    expect(SourceSite.byId('hongguo').id, 'hongguo');
    expect(SourceSite.byId('fanguo').id, 'fanguo');
  });

  test('byId resolves 黄果 group ids, not falling back to 红果', () {
    // 三个黄果入口共享 groupId huangguo；此前 byId('huangguo') 会静默回落到红果，
    // 导致解锁后切到黄果再切走时站源被重置。
    final video = SourceSite.byId('huangguo-video');
    expect(video.groupId, 'huangguo');

    final grouped = SourceSite.byId('huangguo');
    expect(grouped.groupId, 'huangguo');
    expect(grouped.id, isNot('hongguo'));
  });

  test('byId keeps 红果 as the last-resort fallback', () {
    expect(SourceSite.byId('not-a-source').id, 'hongguo');
    expect(SourceSite.byId('').id, 'hongguo');
  });

  test('visibleValues only exposes restricted sources when unlocked', () {
    final locked = SourceSite.visibleValues(unlocked: false);
    final unlocked = SourceSite.visibleValues(unlocked: true);
    if (allSourcesEnabled) {
      expect(locked.every((s) => SourceSite.isPrimary(s.id)), isTrue);
      for (final id in ['huangdou', 'huangguo-video', 'dsd', 'yeguo', 'huangju']) {
        expect(locked.any((s) => s.id == id), isFalse);
        expect(unlocked.any((s) => s.id == id), isTrue);
      }
    } else {
      expect(locked.length, 1);
    }
  });
}
