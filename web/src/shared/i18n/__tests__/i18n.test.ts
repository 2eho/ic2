import { describe, expect, it } from 'vitest';
import { enUS, translate, zhCN } from '../index';

function flatKeys(obj: unknown, prefix = ''): string[] {
  if (obj === null || typeof obj !== 'object') return [prefix];
  const out: string[] = [];
  for (const [k, v] of Object.entries(obj as Record<string, unknown>)) {
    out.push(...flatKeys(v, prefix ? `${prefix}.${k}` : k));
  }
  return out;
}

describe('i18n', () => {
  it('中英文键集完全一致（防止漏配文案）', () => {
    expect(flatKeys(enUS).sort()).toEqual(flatKeys(zhCN).sort());
  });

  it('按路径取文案', () => {
    expect(translate('zh-CN', 'common.save')).toBe('保存');
    expect(translate('en-US', 'common.save')).toBe('Save');
  });

  it('变量替换', () => {
    expect(translate('zh-CN', 'workbench.generateCount', { n: 4 })).toBe('生成 4 张');
  });

  it('缺 key 时返回 key 本身（便于排查）', () => {
    expect(translate('zh-CN', 'nope.missing.key')).toBe('nope.missing.key');
  });

  it('错误码文案齐全（与服务端稳定 code 对齐）', () => {
    const codes = [
      'invalid_request', 'invalid_geometry', 'invalid_node_type', 'invalid_spec', 'invalid_id',
      'not_found', 'conflict', 'unauthorized', 'forbidden', 'rate_limited', 'payload_too_large',
      'ssrf_blocked', 'quota_exceeded', 'upstream_invalid', 'upstream_rate_limited',
      'content_policy', 'interrupted', 'unknown_field', 'internal', 'not_implemented',
      'plugin_permission_denied',
    ];
    for (const c of codes) {
      expect(translate('zh-CN', `errors.${c}`)).not.toBe(`errors.${c}`);
      expect(translate('en-US', `errors.${c}`)).not.toBe(`errors.${c}`);
    }
  });
});
