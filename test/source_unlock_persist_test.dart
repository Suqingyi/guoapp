import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/local_store.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('reload keeps the unlock within the same run', () async {
    SharedPreferences.setMockInitialValues({});
    final store = testStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);

    await store.enableSourceGate('666666');
    expect(store.sourcesUnlocked, isTrue);
    final before = store.sources.map((s) => s.id).toList();

    // media_library 的自动任务、profiles 页面等都会触发 reload。
    await store.reload();

    expect(store.sourcesUnlocked, isTrue,
        reason: 'reload 不应清掉本次运行的解锁状态');
    expect(store.sources.map((s) => s.id).toList(), before,
        reason: '解锁后 reload 站源集合应保持不变');
    if (allSourcesEnabled) {
      expect(store.sources.length, 11);
    }
  });

  test('reload still restores hiding after an explicit lock', () async {
    SharedPreferences.setMockInitialValues({});
    final store = testStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);

    await store.enableSourceGate('666666');
    store.lockSources();
    expect(store.sourcesUnlocked, isFalse);

    await store.reload();
    expect(store.sourcesUnlocked, isFalse,
        reason: '已显式上锁时 reload 不能自动解锁');
    expect(store.sources.length, allSourcesEnabled ? 4 : 1);
  });

  test('a fresh store after restart starts hidden', () async {
    SharedPreferences.setMockInitialValues({});
    final store = testStore(await SharedPreferences.getInstance());
    await store.enableSourceGate('666666');
    expect(store.sourcesUnlocked, isTrue);

    // 模拟重启：新建 store 读取同一份 prefs。
    final restarted = testStore(store.preferences);
    addTearDown(restarted.dispose);
    expect(restarted.sourceGateEnabled, isTrue);
    expect(restarted.sourcesUnlocked, isFalse,
        reason: '重启后应恢复隐藏');
    expect(restarted.sources.length, allSourcesEnabled ? 4 : 1);

    await restarted.unlockSources('666666');
    expect(restarted.sourcesUnlocked, isTrue);
    store.dispose();
  });
}
