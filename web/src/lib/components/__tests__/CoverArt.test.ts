import { flushSync, mount, unmount } from 'svelte';
import { describe, expect, it } from 'vitest';
import CoverArt from '../CoverArt.svelte';

describe('CoverArt', () => {
	it('replaces a failed image with the placeholder spine', () => {
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mount(CoverArt, { target, props: { url: '/covers/1.jpg', title: 'Book A' } });
		flushSync();
		const image = target.querySelector('img');
		expect(image, 'cover image not rendered').not.toBeNull();
		image!.dispatchEvent(new Event('error'));
		flushSync();
		expect(target.querySelector('img')).toBeNull();
		expect(target.querySelector('.cover--placeholder')).not.toBeNull();
		unmount(comp);
		target.remove();
	});
});
