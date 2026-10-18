// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { curate, fieldProblems, headingOnes, labelNameMismatches } from './a11y.testkit';

function dom(html: string): HTMLElement {
	const root = document.createElement('div');
	root.innerHTML = html;
	return root;
}

describe('the jsdom stand-ins for the axe rules', () => {
	it('curates like axe: case, emoji, symbols and punctuation do not matter', () => {
		expect(curate('👎 Rechazar 0/2')).toBe('rechazar 0 2');
		expect(curate('  KT7P: Tocá  para copiar ')).toBe('kt7p tocá para copiar');
	});

	it('flags the three patterns the e2e allow-list used to tolerate', () => {
		const root = dom(`
			<button aria-label="Copiar el código K T 7 P">KT7P</button>
			<button aria-label="60 segundos">60 s</button>
			<button aria-label="Sin límite de tiempo">Libre</button>
			<button aria-label="Rechazar la respuesta de Beto: 0 de 2 votos"><span aria-hidden="true">👎</span> Rechazar 0/2</button>`);
		expect(labelNameMismatches(root)).toEqual([
			'"KT7P" is named "Copiar el código K T 7 P"',
			'"60 s" is named "60 segundos"',
			'"Libre" is named "Sin límite de tiempo"',
			'"👎 Rechazar 0/2" is named "Rechazar la respuesta de Beto: 0 de 2 votos"'
		]);
	});

	it('accepts a name that contains what the control shows, and controls without text or label', () => {
		const root = dom(`
			<button aria-label="KT7P: tocá para copiar">KT7P</button>
			<button>60 s</button>
			<button aria-label="Rechazar 0/2: respuesta de Beto"><span aria-hidden="true">👎</span> Rechazar 0/2</button>
			<button aria-label="Sacar a Ana"><span aria-hidden="true">✕</span></button>
			<button aria-label="Ir a la categoría 2: País"><span aria-hidden="true">2</span></button>
			<p aria-label="not a control">text</p>`);
		expect(labelNameMismatches(root)).toEqual([]);
	});

	it('counts the h1s a screen shows, not the hidden ones', () => {
		expect(headingOnes(dom('<main><h2>x</h2></main>'))).toEqual([]);
		expect(headingOnes(dom('<h1> Ronda  1 </h1><h2>x</h2>'))).toEqual(['Ronda 1']);
		expect(
			headingOnes(dom('<h1>a</h1><div aria-hidden="true"><h1>b</h1></div><h1>c</h1>'))
		).toEqual(['a', 'c']);
	});

	it('reports fields without id, name or autocomplete, and repeated ids', () => {
		expect(
			fieldProblems(dom('<input id="a" name="a" autocomplete="off"><button>ok</button>'))
		).toEqual([]);
		expect(fieldProblems(dom('<input autocomplete="off">'))).toEqual([
			'<input type=text id= name=> has no id',
			'<input type=text id= name=> has no name'
		]);
		expect(fieldProblems(dom('<input id="a" name="a">'))).toEqual([
			'<input type=text id=a name=a> has no autocomplete'
		]);
		expect(
			fieldProblems(
				dom('<input id="a" name="a" autocomplete="off"><input id="a" name="b" autocomplete="off">')
			)
		).toEqual(['<input type=text id=a name=b> repeats an id']);
	});
});
