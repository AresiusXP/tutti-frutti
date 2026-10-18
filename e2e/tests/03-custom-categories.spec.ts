import { expect, test } from '../src/fixtures';
import { answerFor, bastaBy, chooserStops, createRoom, joinByCode, startGame } from '../src/flows';
import { msgPattern } from '../src/messages';

const CUSTOM = 'Superpoder';

test('two built-in categories plus a custom one: the host builds it, everybody plays it', async ({
	app
}) => {
	const ana = await app.player('Ana');
	const beto = await app.player('Beto');
	const everyone = [ana, beto];

	const code = await createRoom(app, ana);
	await joinByCode(beto, code);

	await test.step('the custom name is validated: case, accents and spaces do not make it new', async () => {
		await ana.picker.open();
		await ana.picker.addCustom(CUSTOM);
		await ana.picker.expectSelected([
			'Nombre',
			'País',
			'Animal',
			'Comida',
			'Color',
			'Marca',
			CUSTOM
		]);

		for (const duplicate of ['SÚPERPODER', '  superpoder  ']) {
			await ana.picker.typeCustom(duplicate);
			await expect(ana.picker.customMessage).toHaveText(
				ana.t('lobby_custom_error_dup', { label: CUSTOM })
			);
			await expect(ana.picker.customSubmit).toBeDisabled();
		}
		await ana.picker.typeCustom('Super poder');
		await expect(ana.picker.customSubmit, 'a different word is fine').toBeEnabled();

		// built-in names count too, in both languages ("país" is "País", "Food" is "Comida")
		await ana.picker.typeCustom('pais');
		await expect(ana.picker.customMessage).toHaveText(
			ana.t('lobby_custom_error_dup', { label: 'País' })
		);
		await ana.picker.typeCustom('food');
		await expect(ana.picker.customMessage).toHaveText(msgPattern('es', 'lobby_custom_error_dup'));
		await expect(ana.picker.customSubmit).toBeDisabled();
		await ana.picker.typeCustom('');
	});

	await test.step('exactly 2 built-in + 1 custom (the minimum is 3)', async () => {
		for (const label of ['Nombre', 'Color', 'Comida', 'Marca']) await ana.picker.remove(label);
		await ana.picker.expectSelected(['País', 'Animal', CUSTOM]);
		await expect(ana.page.getByText(ana.t('lobby_cats_at_min', { min: 3 }))).toBeVisible();
		await expect(
			ana.picker.dialog.getByRole('button', {
				name: ana.t('lobby_cat_remove', { label: 'País' }),
				exact: true
			})
		).toBeDisabled();
		await ana.picker.done();
	});

	await test.step('guests see the same categories, the custom one marked', async () => {
		for (const p of everyone) {
			await p.lobby.expectCategories(['País', 'Animal', CUSTOM]);
			await expect(
				p.lobby.categoryChips
					.nth(2)
					.getByRole('img', { name: p.t('lobby_cat_custom_mark'), exact: true })
			).toBeVisible();
		}
	});

	await startGame(ana, everyone);
	await chooserStops(everyone);
	const letter = await ana.playing.letter();

	await test.step('every player gets exactly those three fields', async () => {
		for (const p of everyone) {
			await expect(p.playing.fields).toHaveCount(3);
			for (const [i, label] of ['País', 'Animal', CUSTOM].entries()) {
				await expect(p.playing.fields.nth(i)).toHaveAccessibleName(label);
			}
		}
	});

	await test.step('the custom field is played and reviewed like any other', async () => {
		await ana.playing.fillAll([
			answerFor(letter, 'Ana', 0),
			answerFor(letter, 'Ana', 1),
			`${letter}poder`
		]);
		await beto.playing.fillAll([
			answerFor(letter, 'Beto', 0),
			answerFor(letter, 'Beto', 1),
			`${letter}poder`
		]);
		await bastaBy(ana, [beto]);
		await Promise.all(everyone.map((p) => p.expectScreen('review')));

		await ana.review.jump(3).click();
		for (const p of everyone) {
			await p.review.expectCategory(CUSTOM, 3, 3);
			await p.review.expectAnswer('Ana', `${letter}poder`);
			await p.review.expectAnswer('Beto', `${letter}poder`);
			await p.review.expectPoints('Ana', 5);
			await p.review.expectPoints('Beto', 5);
		}
		await ana.review.finish({ anyway: true });
		for (const p of everyone) await p.summary.expectTotals({ Ana: 25, Beto: 25 });
	});

	await test.step('a rematch keeps the custom category', async () => {
		await ana.summary.endGame();
		await ana.expectScreen('final');
		await ana.final.rematchButton.click();
		for (const p of everyone) {
			await p.expectScreen('lobby');
			await p.lobby.expectCategories(['País', 'Animal', CUSTOM]);
		}
	});
});
