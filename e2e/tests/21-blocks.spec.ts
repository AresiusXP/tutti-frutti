import { expect, test } from '../src/fixtures';
import { createRoom, joinByCode } from '../src/flows';

test.describe.configure({ mode: 'parallel' });

/**
 * Blocking (docs/DESIGN.md, "Safety UI"): "Block and leave" removes the player from the game, the pair can no
 * longer share a room (the refusal never says why) and the block list can undo it.
 */
test('Block and leave: the blocker goes Home, the pair cannot share a room again, and Unblock lets them', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const ben = await app.player('Ben');
	const code = await createRoom(app, ana);
	await joinByCode(ben, code);
	await ana.lobby.expectPlayers(['Ana', 'Ben']);
	let second = '';

	await test.step('the confirmation says what happens before anything is done', async () => {
		await ana.safety.flagPlayer('Ben').click();
		await ana.safety.menuItem('report_menu_block').click();
		await expect(ana.safety.dialog(ana.t('block_title'))).toBeVisible();
		for (const key of ['block_leave', 'block_undo']) {
			await expect(ana.safety.sheet).toContainText(ana.t(key));
		}
		await expect(ana.safety.sheet).toContainText(ana.t('block_blocked', { name: 'Ben' }));
		await expect(ana.safety.sheet).toContainText(ana.t('block_silent', { name: 'Ben' }));
	});

	await test.step('confirming blocks, leaves the game and goes Home with a toast', async () => {
		const answered = ana.page.waitForResponse(
			(r) => r.request().method() === 'POST' && /\/api\/blocks$/.test(r.url())
		);
		await ana.page.getByRole('button', { name: ana.t('block_confirm'), exact: true }).click();
		const response = await answered;
		expect(response.status()).toBe(200);
		expect(response.request().postDataJSON()).toEqual({
			playerId: expect.any(String),
			name: 'Ben'
		});
		expect(((await response.json()) as { leftRooms: string[] }).leftRooms).toEqual([code]);
		await expect(ana.page).toHaveURL(/\/$/);
		await expect(ana.home.toast(ana.t('block_done', { name: 'Ben' }))).toBeVisible();
		// the server removed the seat: no second `leave` from this tab
		expect(ana.sentCount('leave')).toBe(0);
		// the blocked player is not told anything but that the host left
		await ben.lobby.expectPlayers(['Ben']);
	});

	await test.step('the block list shows Ben, and the blocked pair cannot share a new room', async () => {
		await ana.safety.openBlockedList();
		await expect(ana.safety.unblockButton('Ben')).toBeVisible();
		await ana.page.keyboard.press('Escape');
		await ana.page.keyboard.press('Escape');
		second = await createRoom(app, ana);
		await ben.home.open();
		await ben.home.setNickname('Ben');
		await ben.home.submitCode(second);
		// the room check already refuses, in the generic words of every "not now": it never says who blocked whom
		await expect(ben.home.toast(ben.t('home_not_joinable'))).toBeVisible();
		await expect(ben.page.getByText(/block|bloque/i)).toHaveCount(0);
		await expect(ben.page).toHaveURL(/\/$/);
	});

	await test.step('Unblock says so and lets Ben join again', async () => {
		await ana.page.goto('/');
		await ana.safety.openBlockedList();
		await ana.safety.unblockButton('Ben').click();
		await expect(ana.home.toast(ana.t('blocked_unblocked', { name: 'Ben' }))).toBeVisible();
		await expect(ana.safety.unblockButton('Ben')).toHaveCount(0);
		await expect(ana.safety.blockedSheet).toContainText(ana.t('blocked_empty'));
		// nothing joined by itself, but Ben can now take the seat that was refused
		await ben.home.joinButton.click();
		await ben.lobby.expectVisible(second);
	});
});
