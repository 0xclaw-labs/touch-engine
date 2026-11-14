import * as React from 'react';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { ActivityIntro } from '../src/components/customer/ActivityIntro';
import { RewardStatus } from '../src/components/customer/RewardStatus';
import { afterEach, expect, it, vi } from 'vitest';
import { canContinueReservedParticipation, retryParticipationReservation, startParticipation } from '../src/lib/customer-journey';
import { POST } from '../src/app/api/[...path]/route';

afterEach(() => { vi.unstubAllGlobals(); vi.unstubAllEnvs(); });
it('only an actual hold or an explicitly legacy participation can continue', () => {
  for (const state of ['pending', 'failed', 'not_requested', 'unknown', undefined]) {
    expect(canContinueReservedParticipation({ id: 'p1', status: 'pending', reservation_required: true, benefit_reservation_state: state })).toBe(false);
  }
  expect(canContinueReservedParticipation({ id: 'p1', status: 'pending', reservation_required: true, benefit_reservation_state: 'confirmed' })).toBe(true);
  expect(canContinueReservedParticipation({ id: 'p1', status: 'pending', reservation_required: false, benefit_reservation_state: 'not_requested' })).toBe(true);
  expect(canContinueReservedParticipation({ id: 'p1', status: 'pending' })).toBe(false);
});
it('recovery requests the original participation through the real catchall BFF', async () => {
  vi.stubEnv('TOUCH_SERVER_URL', 'http://original-ledger:8080');
  vi.stubEnv('TOUCH_INTERNAL_TOKEN', 'test-token');
  const upstream = vi.fn(async (request: RequestInfo | URL, init?: RequestInit) => {
    expect(String(request)).toBe('http://original-ledger:8080/api/v1/participations/p1/reservation');
    expect(init?.method).toBe('POST');
    expect(new Headers(init?.headers).get('cookie')).toBe('touch_visitor=visitor1');
    return Response.json({ id: 'p1', status: 'pending', reservation_required: true, benefit_reservation_state: 'confirmed' });
  });
  vi.stubGlobal('fetch', upstream);
  const response = await POST(new Request('http://site/api/participations/p1/reservation', { method: 'POST', headers: {cookie:'touch_visitor=visitor1'}, body:'{}' }), { params: Promise.resolve({path:['participations','p1','reservation']}) });
  expect(response.status).toBe(200);
  expect((await response.json()).id).toBe('p1');
  vi.stubGlobal('fetch', vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    expect(String(url)).toBe('/api/participations/p1/reservation');
    expect(init?.method).toBe('POST');
    return Response.json({ id:'p1', status:'pending', reservation_required:true, benefit_reservation_state:'pending' });
  }));
  expect(await retryParticipationReservation('p1')).toMatchObject({id:'p1',benefit_reservation_state:'pending'});
});

it('unconfirmed customer screens offer recovery without a publication or completion prompt', () => {
  vi.stubGlobal('React', React);
  const intro = renderToStaticMarkup(createElement(ActivityIntro, { evidenceKinds:[], existingParticipation:true, reservationReady:false, benefitReservationState:'pending', canStart:false, stockExhausted:true }));
  expect(intro).toContain('确认前不要先完成有奖励任务');
  expect(intro).not.toContain('下方继续发布交接');
  const p = {id:'original-p1',status:'pending',reservation_required:true,benefit_reservation_state:'failed'};
  const status = renderToStaticMarkup(createElement(RewardStatus,{code:'C1',participation:p,benefitsHref:'/c/C1/benefits'}));
  expect(status).toContain('original-p1');
  expect(status).toContain('先恢复原参与的预留');
  expect(status).not.toContain('data-testid="reward-verify"');
  expect(status).not.toContain('data-testid="reward-show-code"');
  expect(status).toContain('href="/c/C1/benefits"');
});

it('an uncertain server result is recovered as the original start rather than declared absent', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => Response.json({message:'upstream unavailable'}, {status:503})));
  expect(await startParticipation('original-start-code','original-work')).toEqual({error:'network'});
});
