(function () {
  'use strict';
  // A read watermark precedes the server's read. Never acknowledge a write's
  // global revision: a concurrent writer may have committed between the two.
  let current, saved, serial = 0, writeVersion = 0;
  const stale = () => Object.assign(new Error('Сверка заменена более новым запросом'), { superseded: true });
  const clone = value => JSON.parse(JSON.stringify(value));
  function valid(tx) {
    return (tx === current || !current && tx.id === serial) && tx.writeVersion === writeVersion && tx.key === location.hash && !tx.cancelled && tx.edit === (window.budgetEditEpoch || 0) && (!tx.background || tx.interaction === (window.budgetInteractionEpoch || 0));
  }
  function coverage(reads) {
    const stamps = [...reads.values()].map(r => r.stamp);
    if (!stamps.length || stamps.some(s => !s || s.generation !== stamps[0].generation)) return null;
    return { generation: stamps[0].generation, revision: Math.min(...stamps.map(s => s.revision)) };
  }
  window.budgetViews = {
    get busy() { return !!current; },
    capture() { return current && valid(current) ? current : null; },
    check(tx) { if (tx && !valid(tx)) throw stale(); },
    rekey() { if (current) current.key = location.hash; },
    invalidate() { writeVersion++; if (current) current.cancelled = true; },
    cached(tx, url) {
      if (!tx || !tx.cache.has(url)) return null;
      const r = tx.cache.get(url); tx.cache.delete(url);
      return { value: clone(r.body), financeRevision: r.financeRevision };
    },
    read(tx, url, body, headers) {
      this.check(tx);
      if (!tx || tx.done) return;
      const generation = headers?.get('X-Budget-Sync-Generation');
      const raw = headers?.get('X-Budget-Sync-Revision');
      const revision = raw === null || raw === undefined ? NaN : Number(raw);
      tx.reads.set(url, { body: clone(body), financeRevision: headers?.get('X-Budget-Revision'), stamp: generation && Number.isSafeInteger(revision) ? {generation, revision} : null });
    },
    written(internal) {
      if (!internal) this.invalidate();
      if (current) current.cache.clear();
    },
    covers(event) {
      const mark = saved?.mark;
      return saved?.key === location.hash && mark && mark.generation === event.generation && mark.revision >= event.revision;
    },
    async run(render, background = false) {
      const tx = {id: ++serial, background, writeVersion, key: location.hash, edit: window.budgetEditEpoch || 0, interaction: window.budgetInteractionEpoch || 0, reads: new Map(), cache: new Map()};
      current = tx;
      const failures = window.budgetReadFailures || 0;
      try {
        if (background && saved?.key === tx.key && saved.reads.size) {
          const old = saved.reads;
          await Promise.all([...old.keys()].map(url => window.budgetAPI(url)));
          this.check(tx);
          const same = [...old].every(([url, r]) => JSON.stringify(r.body) === JSON.stringify(tx.reads.get(url)?.body));
          tx.cache = new Map(tx.reads);
          if (!same) await render();
        } else await render();
        this.check(tx);
        if ((window.budgetReadFailures || 0) !== failures) { saved = null; return false; }
        window.budgetAcceptFinanceRead?.(tx.reads);
        saved = {key: location.hash, reads: tx.reads, mark: coverage(tx.reads)};
        window.dispatchEvent(new Event('budget-view-ready'));
        return true;
      } catch (e) {
        if (!e.superseded) { saved = null; throw e; }
        window.dispatchEvent(new Event('budget-view-stale'));
        return false;
      } finally { tx.done = true; if (current === tx) current = null; }
    }
  };

  // Finance uses HTML templates and rebinds on* handlers after each render.
  // Keep matching elements (including keyed table rows) and change only text,
  // attributes and structure. Planner closures are NOT transplanted this way.
  function key(n) { return n.nodeType === 1 ? n.id || ['expenseId','incomeId'].map(k => n.dataset[k] ? k+':'+n.dataset[k] : '').join('') : ''; }
  function patch(old, fresh) {
    if (old.nodeType !== fresh.nodeType || old.nodeName !== fresh.nodeName) { old.replaceWith(fresh); return; }
    if (old.nodeType !== 1) { if (old.nodeValue !== fresh.nodeValue) old.nodeValue = fresh.nodeValue; return; }
    if (old.nodeName === 'CANVAS') return; // Chart owns its backing-store dimensions.
    for (const a of [...old.attributes]) if (!fresh.hasAttribute(a.name)) old.removeAttribute(a.name);
    for (const a of fresh.attributes) if (old.getAttribute(a.name) !== a.value) old.setAttribute(a.name, a.value);
    if (old instanceof HTMLInputElement) { if (old.value !== fresh.value) old.value = fresh.value; old.checked = fresh.checked; }
    const selected = fresh instanceof HTMLSelectElement ? fresh.value : null;
    children(old, fresh);
    if (selected !== null) old.value = selected;
  }
  function children(old, fresh) {
    const indexed = new Map([...old.childNodes].filter(key).map(n => [key(n), n]));
    let cursor = old.firstChild;
    for (const n of [...fresh.childNodes]) {
      const k = key(n), match = k ? indexed.get(k) : cursor && !key(cursor) && cursor.nodeName === n.nodeName ? cursor : null;
      if (match) { if (match !== cursor) old.insertBefore(match, cursor); patch(match, n); cursor = match.nextSibling; }
      else old.insertBefore(n, cursor);
    }
    while (cursor) { const next = cursor.nextSibling; cursor.remove(); cursor = next; }
  }
  window.budgetHTML = (root, html) => { const fresh = document.createElement('div'); fresh.innerHTML = html; children(root, fresh); };
})();
