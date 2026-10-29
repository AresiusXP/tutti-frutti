import type { Locator } from '@playwright/test';
import type { PublicRoomsResponse } from '../../web/src/lib/protocol/api';
import { expectAccessible } from '../src/a11y';
import { expect, test } from '../src/fixtures';
import { expectUnobscured } from '../src/layout';
import { expectNoSpanish } from '../src/no-spanish';
import type { Player } from '../src/player';
import { createRoomAPI, joinRoomSocket } from '../src/rooms';

async function expectPhoneScreen(player: Player, screen: string): Promise<void> {
	// The standard axe helper also requires exactly one accessible h1.
	await expectAccessible(player, screen);
	await expect
		.poll(
			() =>
				player.page.evaluate(() =>
					Math.max(
						...[document.documentElement, document.body, ...document.querySelectorAll('main')].map(
							(element) => element.scrollWidth - element.clientWidth
						)
					)
				),
			{ message: `${screen} has no horizontal overflow at 360×740` }
		)
		.toBeLessThanOrEqual(0);
	if (player.locale !== 'es') await expectNoSpanish([player], player.locale, screen);
}

async function expectTouchTarget(control: Locator, name: string): Promise<void> {
	await control.scrollIntoViewIfNeeded();
	await expectUnobscured(control, name);
	const box = await control.boundingBox();
	expect(box, `${name} has a rendered target`).not.toBeNull();
	expect(box!.width, `${name} target width`).toBeGreaterThanOrEqual(48);
	expect(box!.height, `${name} target height`).toBeGreaterThanOrEqual(48);
}

async function expectBrowseControls(player: Player): Promise<void> {
	const controls = player.rooms.main.getByRole('button').or(player.rooms.main.getByRole('link'));
	for (const control of await controls.all()) {
		await expectTouchTarget(control, (await control.innerText()).trim());
	}
}

for (const locale of ['es', 'en', 'nl'] as const) {
	test(`${locale}: public discovery and creation are accessible on a 360×740 phone`, async ({
		app
	}) => {
		const host = await app.player('Host', { locale });
		const code = await createRoomAPI(app, host, 'public');
		const visitor = await app.player('Visitor', {
			locale,
			viewport: { width: 360, height: 740 },
			reducedMotion: true,
			serviceWorkers: 'block'
		});

		// Fetch the real directory, projecting it onto this fixture's code so other tests' rooms
		// cannot spoil the empty state or leak their custom category text into the locale audit.
		// No room data is fabricated: the host must really join before its card can appear.
		await visitor.page.route(/\/api\/rooms(?:\?.*)?$/, async (route) => {
			const response = await route.fetch();
			expect(response.ok()).toBe(true);
			const body = (await response.json()) as PublicRoomsResponse;
			await route.fulfill({
				response,
				json: { ...body, rooms: body.rooms.filter((room) => room.code === code) }
			});
		});

		await test.step('empty browse screen and its public-create link', async () => {
			await visitor.rooms.open();
			await expect(visitor.page.getByText(visitor.t('rooms_empty'), { exact: true })).toBeVisible();
			await expect(visitor.rooms.main.getByRole('listitem')).toHaveCount(0);
			await expectPhoneScreen(visitor, 'empty public rooms');
			await expectBrowseControls(visitor);
		});

		await test.step('public creation is preselected and both visibility choices are usable', async () => {
			await visitor.page
				.getByRole('link', { name: visitor.t('rooms_create'), exact: true })
				.click();
			await expect(visitor.home.nickname).toBeVisible();
			await visitor.home.setNickname(visitor.name);
			const visibility = visitor.page.getByRole('group', {
				name: visitor.t('rooms_visibility'),
				exact: true
			});
			await expect(
				visibility.getByRole('button', { name: visitor.t('rooms_public'), exact: true })
			).toHaveAttribute('aria-pressed', 'true');
			await visitor.home.setVisibility('private');
			await expect(visitor.rooms.casualCaption).toHaveCount(0);
			await visitor.home.setVisibility('public');
			await expect(visitor.rooms.casualCaption).toBeVisible();
			await expectPhoneScreen(visitor, 'Home with public creation selected');
			await expect
				.poll(
					() =>
						visitor.page.evaluate(
							() =>
								Math.max(document.documentElement.scrollHeight, document.body.scrollHeight) -
								window.innerHeight
						),
					{ message: 'Public-selected Home fits one 360×740 screen without vertical scrolling' }
				)
				.toBeLessThanOrEqual(1);
			for (const control of await visibility.getByRole('button').all()) {
				await expectTouchTarget(control, (await control.innerText()).trim());
			}
			await expectTouchTarget(visitor.home.createButton, visitor.t('create_game'));
		});

		await test.step('a claimed public lobby renders a readable, usable browse card', async () => {
			const socket = await joinRoomSocket(app, host, code);
			await visitor.rooms.open();
			await expect(visitor.rooms.joinButton(code)).toBeVisible();
			await expect(visitor.rooms.main.getByRole('listitem')).toHaveCount(1);
			await expect(visitor.page.getByText(visitor.t('rooms_empty'), { exact: true })).toHaveCount(
				0
			);
			await expect(visitor.rooms.card(code)).toContainText(
				visitor.t('rooms_time', { seconds: 90 })
			);
			for (const label of socket.state!.view.categoryLabels) {
				await expect(visitor.rooms.card(code)).toContainText(label);
			}
			await expectPhoneScreen(visitor, 'populated public rooms');
			await expectBrowseControls(visitor);
		});
	});
}
