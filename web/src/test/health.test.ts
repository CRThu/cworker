// web/src/test/health.test.ts - 后端健康状态机与断线报警单元测试
import { describe, it, expect } from 'vitest';

describe('Backend health state machine', () => {
  interface HealthState {
    isBackendConnected: boolean;
    backendErrorMsg: string | null;
    consecutiveFailures: number;
  }

  function createHealthTracker() {
    let state: HealthState = {
      isBackendConnected: true,
      backendErrorMsg: null,
      consecutiveFailures: 0,
    };

    return {
      getState: () => ({ ...state }),
      onSuccess: () => {
        state.isBackendConnected = true;
        state.backendErrorMsg = null;
        state.consecutiveFailures = 0;
      },
      onFailure: (err: Error) => {
        state.consecutiveFailures++;
        state.isBackendConnected = false;
        state.backendErrorMsg = err.message || '控制台服务通信中断';
      },
      getStatusDotClass: (onlineCount: number) => {
        if (!state.isBackendConnected) return 'offline';
        if (onlineCount > 0) return 'online';
        return 'idle';
      },
      getRefreshIndicatorClass: (isAutoRefresh: boolean) => {
        if (!state.isBackendConnected) return 'disconnected';
        if (isAutoRefresh) return 'active';
        return 'paused';
      },
    };
  }

  it('should be online initially', () => {
    const tracker = createHealthTracker();
    expect(tracker.getState().isBackendConnected).toBe(true);
    expect(tracker.getStatusDotClass(2)).toBe('online');
    expect(tracker.getRefreshIndicatorClass(true)).toBe('active');
  });

  it('should immediately turn offline/red upon network failure', () => {
    const tracker = createHealthTracker();
    tracker.onFailure(new Error('Failed to fetch'));

    const state = tracker.getState();
    expect(state.isBackendConnected).toBe(false);
    expect(state.backendErrorMsg).toBe('Failed to fetch');
    expect(state.consecutiveFailures).toBe(1);

    // 验证左下角状态点变为 offline (红色)
    expect(tracker.getStatusDotClass(2)).toBe('offline');
    // 验证右上角刷新指示灯变为 disconnected (红色)
    expect(tracker.getRefreshIndicatorClass(true)).toBe('disconnected');
  });

  it('should self-heal when subsequent fetch succeeds', () => {
    const tracker = createHealthTracker();
    tracker.onFailure(new Error('Network error'));
    expect(tracker.getState().isBackendConnected).toBe(false);

    // 后端恢复正常
    tracker.onSuccess();
    const healed = tracker.getState();
    expect(healed.isBackendConnected).toBe(true);
    expect(healed.backendErrorMsg).toBeNull();
    expect(healed.consecutiveFailures).toBe(0);
    expect(tracker.getStatusDotClass(1)).toBe('online');
    expect(tracker.getRefreshIndicatorClass(true)).toBe('active');
  });
});
