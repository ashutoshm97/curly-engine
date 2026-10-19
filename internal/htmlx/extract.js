(() => {
  const pick = () => {
    const sels = ['.reveal .slides > section', '[data-slide]', '.slide', 'body > section', 'main > section'];
    for (const s of sels) {
      const els = Array.from(document.querySelectorAll(s));
      if (els.length) return els;
    }
    // Fallback: full-viewport siblings, else the body as one slide.
    const kids = Array.from(document.body.children).filter(e => {
      const r = e.getBoundingClientRect();
      return r.width >= innerWidth * 0.9 && r.height >= innerHeight * 0.8;
    });
    return kids.length ? kids : [document.body];
  };
  const slides = pick();
  const st = document.createElement('style');
  st.textContent = `.__se_show{display:block!important;visibility:visible!important;opacity:1!important;transform:none!important;position:relative!important;left:0!important;top:0!important}`;
  document.head.appendChild(st);
  const out = [];
  const visible = (cs) => cs.display !== 'none' && cs.visibility !== 'hidden' && parseFloat(cs.opacity) > 0.01;
  const bgOf = (el) => {
    for (let e = el; e; e = e.parentElement) {
      const c = getComputedStyle(e).backgroundColor;
      if (c && c !== 'transparent' && !/rgba\(.*,\s*0\)$/.test(c)) return c;
    }
    return 'rgb(255, 255, 255)';
  };
  const style = (cs) => ({
    font: cs.fontFamily, size: parseFloat(cs.fontSize), weight: parseInt(cs.fontWeight) || 400,
    italic: cs.fontStyle === 'italic', color: cs.color, align: cs.textAlign,
  });
  const ownText = (el) => Array.from(el.childNodes).some(n => n.nodeType === 3 && n.textContent.trim());
  for (const [i, s] of slides.entries()) {
    s.classList.add('__se_show');
    s.scrollIntoView();
    const sr = s.getBoundingClientRect();
    const box = (r) => ({ x: (r.left - sr.left) / sr.width, y: (r.top - sr.top) / sr.height, w: r.width / sr.width, h: r.height / sr.height });
    const els = [];
    const walk = (el) => {
      for (const c of el.children) {
        const cs = getComputedStyle(c);
        if (!visible(cs)) continue;
        const r = c.getBoundingClientRect();
        if (r.width < 2 || r.height < 2) { walk(c); continue; }
        const tag = c.tagName.toLowerCase();
        const bg = cs.backgroundColor;
        const hasBg = bg && bg !== 'transparent' && !/rgba\(.*,\s*0\)$/.test(bg);
        if (tag === 'img' || tag === 'svg' || tag === 'canvas' || tag === 'video') {
          els.push({ kind: 'image', box: box(r), src: c.currentSrc || c.src || '', radius: parseFloat(cs.borderTopLeftRadius) || 0 });
          continue;
        }
        if (tag === 'table') {
          const rows = Array.from(c.rows).slice(0, 30);
          const cells = rows.map(tr => Array.from(tr.cells).map(td => td.innerText.trim()));
          const rs = (k) => rows[k] ? getComputedStyle(rows[k].cells[0] || rows[k]) : null;
          const h = rs(0), b1 = rs(1), b2 = rs(2);
          els.push({ kind: 'table', box: box(r), cells,
            headerFill: h ? bgOf(rows[0].cells[0]) : '', headerText: h ? h.color : '', headerBold: h ? (parseInt(h.fontWeight) >= 600) : false,
            bodyFill: b2 ? bgOf(rows[2].cells[0]) : (b1 ? bgOf(rows[1].cells[0]) : ''), bandFill: b1 ? bgOf(rows[1].cells[0]) : '',
            bodyText: b1 ? b1.color : '', size: b1 ? parseFloat(b1.fontSize) : (h ? parseFloat(h.fontSize) : 0),
            border: cs.borderTopColor });
          continue;
        }
        if (hasBg && c !== s) {
          els.push({ kind: 'shape', box: box(r), fill: bg, radius: parseFloat(cs.borderTopLeftRadius) || 0, shadow: cs.boxShadow !== 'none' });
        }
        if (tag === 'ul' || tag === 'ol') {
          const items = Array.from(c.querySelectorAll('li')).map(li => li.innerText.trim()).filter(Boolean);
          const li = c.querySelector('li');
          els.push({ kind: 'text', box: box(r), text: items.join('\n'), paragraphs: items.length,
            bullet: tag === 'ol' ? '1.' : '•', style: style(li ? getComputedStyle(li) : cs), tag });
          continue;
        }
        if (ownText(c)) {
          const t = c.innerText.trim();
          if (t) {
            els.push({ kind: 'text', box: box(r), text: t, paragraphs: t.split('\n').filter(Boolean).length, style: style(cs), tag });
            continue;
          }
        }
        walk(c);
      }
    };
    walk(s);
    out.push({ index: i + 1, width: sr.width, height: sr.height, background: bgOf(s), elements: els });
    s.classList.remove('__se_show');
  }
  return JSON.stringify(out);
})()
