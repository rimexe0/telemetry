// Small, dependency-free motion for the Telemetry site.
(() => {
  const root = document.documentElement;
  const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;

  // A new pastel hue on every visit, like rime.cyou.
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

  // Wordmark letters fly in one after another.
  const letters = document.querySelectorAll('.wordmark .front .letter, .wordmark .back .letter');
  const fronts = [...document.querySelectorAll('.wordmark .front .letter')];
  const backs = [...document.querySelectorAll('.wordmark .back .letter')];
  if (reduce) {
    root.classList.add('intro-done');
  } else {
    requestAnimationFrame(() => {
      fronts.forEach((letter, i) => {
        setTimeout(() => {
          letter.classList.add('in');
          backs[i]?.classList.add('in');
        }, 120 + i * 70);
      });
      setTimeout(() => root.classList.add('intro-shown'), 550);
      setTimeout(() => root.classList.add('intro-done'), 120 + letters.length * 70 + 900);
    });
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

  // The logo rows follow the mouse a little, and calm down while reading.
  const rowA = document.querySelector('.row.a');
  const rowB = document.querySelector('.row.b');
  if (rowA && rowB && !reduce) {
    let frame = 0, x = 0, y = 0;
    addEventListener('mousemove', (event) => {
      x = (event.clientX - innerWidth / 2) / 50;
      y = (event.clientY - innerHeight / 2) / 50;
      if (frame) return;
      frame = requestAnimationFrame(() => {
        frame = 0;
        rowA.style.transform = `translate(${x}px, ${y}px)`;
        rowB.style.transform = `translate(${-x}px, ${-y}px)`;
      });
    }, { passive: true });
  }
  const hero = document.querySelector('.hero, .page-head');
  const nav = document.querySelector('.side-nav');
  const onScroll = () => {
    const past = hero ? hero.getBoundingClientRect().bottom < innerHeight * 0.35 : true;
    root.classList.toggle('calm', past);
    nav?.classList.toggle('hidden', !past && !!document.querySelector('.hero'));
  };
  addEventListener('scroll', onScroll, { passive: true });
  onScroll();

  // Side nav marks the section on screen.
  const links = [...document.querySelectorAll('.side-nav a[href^="#"]')];
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
