// Small, dependency-free motion for the Telemetry site.
(() => {
  const root = document.documentElement;
  const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;
  const started = performance.now();

  // A new pastel hue on every visit.
  root.style.setProperty('--bg-hue', String(Math.floor(Math.random() * 360)));

  // Card hues come from their titles, so each card keeps its color.
  const hue = (seed) => {
    let hash = 0;
    for (const ch of seed) hash = (hash * 31 + ch.charCodeAt(0)) % 360;
    return (hash * 70 + 340) % 360;
  };
  document.querySelectorAll('.card[data-hue]').forEach((card) => {
    card.style.setProperty('--h', String(hue(card.dataset.hue)));
  });

  // Background: lanes of made-up activity drifting past, like a day timeline.
  const lanes = document.querySelector('.lanes');
  if (lanes) {
    const colors = ['#3ddc84', '#3ddc84', '#4dc3ff', '#4dc3ff', '#ff5c5c', '#ffd24d', '#8a5cff', '#9aa0a6'];
    const pick = (list) => list[Math.floor(Math.random() * list.length)];
    const rand = (a, b) => a + Math.random() * (b - a);
    const count = Math.ceil(Math.max(screen.height, innerHeight) / 58) + 1;
    for (let i = 0; i < count; i++) {
      const width = Math.round(rand(900, 1500));
      let x = rand(0, 40), bars = '';
      while (x < width - 30) {
        const length = Math.min(rand(16, 230), width - x - 10);
        bars += `<rect x="${x.toFixed(1)}" y="8" width="${length.toFixed(1)}" height="10" rx="3" fill="${pick(colors)}"/>`;
        x += length + rand(6, 60);
      }
      const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="26">${bars}</svg>`;
      const lane = document.createElement('div');
      lane.className = 'lane' + (i % 2 ? ' rev' : '');
      lane.style.top = `${i * 58 + rand(-8, 8)}px`;
      lane.style.backgroundImage = `url("data:image/svg+xml,${encodeURIComponent(svg)}")`;
      lane.style.setProperty('--w', `${width}px`);
      lane.style.animationDuration = `${(width / rand(10, 28)).toFixed(1)}s`;
      lanes.appendChild(lane);
    }
  }

  // The wordmark types itself out.
  const letters = [...document.querySelectorAll('.wordmark .letter')];
  if (reduce) {
    root.classList.add('intro-shown', 'intro-done');
  } else {
    let delay = 150;
    letters.forEach((letter) => {
      delay += 55 + Math.random() * 70;
      setTimeout(() => letter.classList.add('in'), delay);
    });
    setTimeout(() => root.classList.add('intro-shown'), 450);
    setTimeout(() => root.classList.add('intro-done'), delay + 200);
  }

  // REC counter: how long the page has been watching you.
  const rec = document.querySelector('.rec-time');
  if (rec) {
    const pad = (n) => String(n).padStart(2, '0');
    const tick = () => {
      const s = Math.floor((performance.now() - started) / 1000);
      rec.textContent = `${pad(Math.floor(s / 3600))}:${pad(Math.floor(s / 60) % 60)}:${pad(s % 60)}`;
    };
    tick();
    setInterval(tick, 1000);
  }

  // Cards rise in as they come into view.
  const reveals = document.querySelectorAll('.reveal');
  reveals.forEach((el) => {
    el.querySelectorAll('.item, .body').forEach((child, i) => child.style.setProperty('--i', String(Math.min(i, 8))));
  });
  if ('IntersectionObserver' in window && !reduce) {
    const io = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (entry.isIntersecting) {
          entry.target.classList.add('in');
          io.unobserve(entry.target);
        }
      }
    }, { rootMargin: '0px 0px -12% 0px', threshold: 0.05 });
    reveals.forEach((el) => io.observe(el));
  } else {
    reveals.forEach((el) => el.classList.add('in'));
  }

  // Background calms down while you read; the nav appears after the hero.
  const hero = document.querySelector('.hero, .page-head');
  const nav = document.querySelector('.top-nav');
  const onScroll = () => {
    const past = hero ? hero.getBoundingClientRect().bottom < innerHeight * 0.35 : true;
    root.classList.toggle('calm', past);
    nav?.classList.toggle('hidden', !past && !!document.querySelector('.hero'));
  };
  addEventListener('scroll', onScroll, { passive: true });
  onScroll();

  // Nav marks the section on screen.
  const links = [...document.querySelectorAll('.top-nav a[href^="#"]')];
  const sections = links.map((a) => document.querySelector(a.getAttribute('href'))).filter(Boolean);
  if ('IntersectionObserver' in window && sections.length) {
    const spy = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        links.forEach((a) => a.classList.toggle('active', a.getAttribute('href') === '#' + entry.target.id));
      }
    }, { rootMargin: '-45% 0px -50% 0px' });
    sections.forEach((s) => spy.observe(s));
  }

  // Big silly numbers that never stop, until you stare at them too long:
  // then the box spins up like a washing machine, stops dead and fades.
  const stats = document.querySelector('.stats');
  if (stats) {
    const tiles = [...stats.querySelectorAll('.stat')];
    const numbers = [...stats.querySelectorAll('b')];
    let scrolled = 0, lastY = scrollY, frozen = false;
    addEventListener('scroll', () => { scrolled += Math.abs(scrollY - lastY); lastY = scrollY; }, { passive: true });
    const format = (value, digits) => value.toLocaleString('en-US', { minimumFractionDigits: digits, maximumFractionDigits: digits });
    const draw = () => {
      if (frozen) return;
      const t = (performance.now() - started) / 1000;
      for (const b of numbers) {
        const digits = Number(b.dataset.digits || 0);
        let value = 0;
        if (b.dataset.rate) value = Number(b.dataset.rate) * t;
        else if (b.dataset.here === 'seconds') value = t;
        else if (b.dataset.here === 'scroll') value = scrolled * 2.54 / 96;
        b.textContent = format(value, digits);
      }
      requestAnimationFrame(draw);
    };
    requestAnimationFrame(draw);

    const LINGER = 6000, RAMP = 9000, PEAK = 1300;
    let seen = 0, visible = false, last = performance.now(), state = 'idle';
    if ('IntersectionObserver' in window) {
      new IntersectionObserver(([entry]) => { visible = entry.intersectionRatio >= 0.5; }, { threshold: [0, 0.5, 1] }).observe(stats);
    }
    const finish = () => {
      state = 'done';
      frozen = true;
      stats.style.transition = 'filter 1400ms ease, opacity 1400ms ease';
      stats.style.transform = 'none';
      tiles.forEach((tile) => { tile.style.transform = ''; });
      const done = stats.querySelector('.spin-done');
      if (done) done.hidden = false;
      requestAnimationFrame(() => requestAnimationFrame(() => stats.classList.add('spent')));
    };
    const spin = (startedAt) => {
      let phase = 0, jolt = 0, prev = startedAt;
      stats.style.transition = 'none';
      const step = (now) => {
        const elapsed = now - startedAt;
        const dt = Math.min(0.05, (now - prev) / 1000);
        prev = now;
        if (elapsed > RAMP + PEAK) { finish(); return; }
        const p = Math.min(1, elapsed / RAMP);
        const amp = 0.6 + 11 * Math.pow(p, 1.7);           // px
        const freq = 1.5 + 15 * Math.pow(p, 1.3);          // Hz: the drum spinning up
        phase += 2 * Math.PI * freq * dt;
        if (Math.random() < 0.02 + 0.12 * p) jolt = (Math.random() - 0.5) * amp * 1.6; // an off-balance thump
        jolt *= 0.86;
        const x = amp * Math.sin(phase) + jolt;
        const y = amp * 0.45 * Math.sin(phase * 2 + 0.7);
        const r = amp * 0.09 * Math.sin(phase * 0.5);
        stats.style.transform = `translate(${x.toFixed(2)}px, ${y.toFixed(2)}px) rotate(${r.toFixed(2)}deg)`;
        tiles.forEach((tile) => {
          const k = amp * 0.25;
          tile.style.transform = `translate(${((Math.random() - 0.5) * k).toFixed(2)}px, ${((Math.random() - 0.5) * k).toFixed(2)}px)`;
        });
        requestAnimationFrame(step);
      };
      requestAnimationFrame(step);
    };
    const watch = (now) => {
      const dt = now - last;
      last = now;
      if (state === 'idle') {
        if (visible && document.visibilityState === 'visible') seen += dt;
        if (seen >= LINGER) {
          state = 'spinning';
          if (reduce) finish(); else spin(now);
          return;
        }
      }
      requestAnimationFrame(watch);
    };
    requestAnimationFrame(watch);
  }

  // Copy buttons on code blocks.
  document.querySelectorAll('.code').forEach((block) => {
    const pre = block.querySelector('pre');
    if (!pre) return;
    const button = document.createElement('button');
    button.className = 'copy';
    button.type = 'button';
    button.textContent = 'copy';
    button.addEventListener('click', async () => {
      const text = [...pre.querySelectorAll('[data-copy]')].map((n) => n.textContent).join('\n') || pre.innerText;
      try {
        await navigator.clipboard.writeText(text.trim());
        button.textContent = 'copied';
        button.classList.add('done');
      } catch {
        button.textContent = 'select it';
      }
      setTimeout(() => { button.textContent = 'copy'; button.classList.remove('done'); }, 1600);
    });
    block.appendChild(button);
  });

  // Screenshot lightbox.
  const dialog = document.querySelector('dialog.lightbox');
  if (dialog) {
    const img = dialog.querySelector('img');
    const caption = dialog.querySelector('span');
    document.querySelectorAll('.shot button').forEach((button) => {
      button.addEventListener('click', () => {
        const source = button.querySelector('img');
        img.src = source.src;
        img.alt = source.alt;
        caption.textContent = button.closest('figure')?.querySelector('figcaption')?.textContent ?? '';
        dialog.showModal();
      });
    });
    dialog.addEventListener('click', (event) => {
      if (event.target === dialog || event.target.closest('button')) dialog.close();
    });
  }
})();
