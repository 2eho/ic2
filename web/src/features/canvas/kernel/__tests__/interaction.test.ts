import { describe, expect, it } from 'vitest';
import { InteractionMachine, type Modifiers } from '../interaction';

const noMod: Modifiers = { shift: false, ctrl: false, meta: false, alt: false, space: false };

describe('InteractionMachine', () => {
  it('命中节点进入拖拽', () => {
    const m = new InteractionMachine();
    const intent = m.handle({ type: 'pointerdown', point: { x: 1, y: 1 }, hitNode: 'n1', modifiers: noMod });
    expect(m.current).toBe('dragging-node');
    expect(intent).toMatchObject({ type: 'start-drag', ids: ['n1'] });
  });

  it('空格或 Ctrl 拖拽进入平移', () => {
    const m = new InteractionMachine();
    m.handle({ type: 'pointerdown', point: { x: 0, y: 0 }, modifiers: { ...noMod, space: true } });
    expect(m.current).toBe('panning');
    const m2 = new InteractionMachine();
    m2.handle({ type: 'pointerdown', point: { x: 0, y: 0 }, modifiers: { ...noMod, ctrl: true } });
    expect(m2.current).toBe('panning');
  });

  it('空白处拖拽进入框选并在抬起时提交', () => {
    const m = new InteractionMachine();
    m.handle({ type: 'pointerdown', point: { x: 0, y: 0 }, modifiers: noMod });
    expect(m.current).toBe('marquee');
    expect(m.handle({ type: 'pointermove', point: { x: 50, y: 50 }, modifiers: noMod })).toMatchObject({ type: 'update-marquee' });
    expect(m.handle({ type: 'pointerup', point: { x: 50, y: 50 } })).toMatchObject({ type: 'commit-marquee' });
    expect(m.current).toBe('idle');
  });

  it('缩放手柄优先于节点拖拽', () => {
    const m = new InteractionMachine();
    const intent = m.handle({ type: 'pointerdown', point: { x: 5, y: 5 }, hitNode: 'n1', hitHandle: 'se', modifiers: noMod });
    expect(m.current).toBe('resizing-node');
    expect(intent).toMatchObject({ type: 'start-resize', handle: 'se' });
  });

  it('拖拽产出相对位移', () => {
    const m = new InteractionMachine();
    m.handle({ type: 'pointerdown', point: { x: 100, y: 100 }, hitNode: 'n1', modifiers: noMod });
    expect(m.handle({ type: 'pointermove', point: { x: 130, y: 90 }, modifiers: noMod })).toMatchObject({ type: 'drag', dx: 30, dy: -10 });
  });

  it('Esc 清空选择并复位', () => {
    const m = new InteractionMachine();
    m.handle({ type: 'pointerdown', point: { x: 0, y: 0 }, hitNode: 'n1', modifiers: noMod });
    expect(m.handle({ type: 'keydown', key: 'Escape', modifiers: noMod })).toEqual({ type: 'clear-selection' });
    expect(m.current).toBe('idle');
  });

  it('双击节点进入文本编辑', () => {
    const m = new InteractionMachine();
    expect(m.handle({ type: 'dblclick', point: { x: 0, y: 0 }, hitNode: 'n1' })).toEqual({ type: 'enter-text-edit', id: 'n1' });
    expect(m.isEditingText).toBe(true);
  });

  it('失焦复位状态', () => {
    const m = new InteractionMachine();
    m.handle({ type: 'pointerdown', point: { x: 0, y: 0 }, hitNode: 'n1', modifiers: noMod });
    m.handle({ type: 'blur' });
    expect(m.current).toBe('idle');
  });

  it('滚轮产出缩放意图', () => {
    const m = new InteractionMachine();
    expect(m.handle({ type: 'wheel', point: { x: 10, y: 10 }, deltaY: 0.1, modifiers: noMod })).toEqual({
      type: 'zoom', point: { x: 10, y: 10 }, delta: 0.1,
    });
  });
});
