'use strict';

const form = document.getElementById('register-form');
const submit = document.getElementById('submit');
const error = document.getElementById('form-error');
let membership;

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  if (submit.disabled) return;
  error.hidden = true;
  const name = form.elements.name.value.trim();
  const email = form.elements.email.value.trim();
  if (!name || new TextEncoder().encode(name).length > 200 || new TextEncoder().encode(email).length > 254) {
    error.textContent = 'Please enter your name and shorten any unusually long name or email address.';
    error.hidden = false;
    return;
  }
  submit.disabled = true;
  submit.textContent = 'Creating your membership…';
  form.setAttribute('aria-busy', 'true');
  try {
    const response = await fetch(form.action, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ name, email }),
    });
    if (!response.ok) {
      const messages = {
        400: 'Please check your name and email address and try again.',
        409: 'You’re already a member. Open your personal member link, or ask the membership team to send you one.',
        429: 'Too many attempts. Please wait a moment and try again.',
      };
      throw new Error(messages[response.status] || 'We couldn’t create your membership right now. Please try again later.');
    }
    const result = await response.json();
    showMembership(result.customer);
    window.history.replaceState(null, '', new URL('./coupons', window.location.href).href);
    form.reset();
    await loadRewards(true);
  } catch (failure) {
    error.textContent = failure instanceof TypeError
      ? 'We couldn’t confirm registration. Check your connection. If a retry says your email is registered, contact the membership team.'
      : failure.message;
    error.hidden = false;
  } finally {
    submit.disabled = false;
    submit.textContent = 'Join the member club ↗';
    form.removeAttribute('aria-busy');
  }
});

function showMembership(customer) {
  membership = { customer };
  document.title = document.title.replace(/^Join the club/, 'Your coupons');
  document.getElementById('member-name').textContent = customer.name;
  document.getElementById('member-email').textContent = customer.email;
  document.getElementById('member-points').textContent = customer.points.toLocaleString();
  document.getElementById('registration').hidden = true;
  document.getElementById('confirmation').hidden = false;
  document.getElementById('member-error').hidden = true;
  document.getElementById('success-title').focus();
}

const coupons = new Map();
const pendingRedemptions = new Map();
let couponPage = 0;
let moreCoupons = false;
let loadingRewards = false;
let redeeming = false;
let balanceKnown = true;
const couponList = document.getElementById('coupon-list');
const refreshButton = document.getElementById('refresh-rewards');
const moreButton = document.getElementById('load-more');
const rewardsError = document.getElementById('rewards-error');
const redemptionStatus = document.getElementById('redemption-status');

async function customerRequest(path, options = {}) {
  return fetch(new URL(`../api/v1/${path}`, window.location.href), {
    ...options,
    credentials: 'same-origin',
    headers: {
      ...options.headers,
    },
  });
}

function renderCoupons() {
  couponList.replaceChildren();
  for (const coupon of coupons.values()) {
    const card = document.getElementById('coupon-template').content.cloneNode(true);
    card.querySelector('.coupon-name').textContent = coupon.name;
    card.querySelector('.coupon-description').textContent = coupon.description;
    card.querySelector('.coupon-cost').textContent = `${coupon.cost.toLocaleString()} pts`;
    card.querySelector('.coupon-stock').textContent = coupon.stock > 0 ? `${coupon.stock.toLocaleString()} left` : 'Sold out';
    const button = card.querySelector('.redeem-button');
    const help = card.querySelector('.coupon-help');
    const pending = pendingRedemptions.has(coupon.id);
    const missing = Math.max(0, coupon.cost - membership.customer.points);
    button.disabled = redeeming || loadingRewards || (!pending && (!balanceKnown || coupon.stock < 1 || missing > 0));
    button.setAttribute('aria-label', `${pending ? 'Retry redemption for' : 'Redeem'} ${coupon.name} for ${coupon.cost.toLocaleString()} points`);
    if (pending) {
      button.textContent = 'Check / retry redemption';
      help.textContent = 'Your last attempt is unconfirmed. Retry safely without spending points twice.';
    } else if (!balanceKnown) {
      help.textContent = 'Refresh your points to redeem this coupon.';
    } else if (coupon.stock < 1) {
      button.textContent = 'Sold out';
      help.textContent = 'All claimed for now. Check back soon.';
    } else if (missing > 0) {
      button.textContent = 'Redeem coupon ↗';
      help.textContent = `Earn ${missing.toLocaleString()} more points to redeem.`;
    }
    button.addEventListener('click', () => redeemCoupon(coupon));
    couponList.append(card);
  }
  document.getElementById('rewards-empty').hidden = coupons.size > 0 || loadingRewards;
  moreButton.hidden = !moreCoupons;
  moreButton.disabled = loadingRewards || redeeming;
  refreshButton.disabled = loadingRewards || redeeming;
}

async function loadRewards(reset) {
  if (loadingRewards) return;
  loadingRewards = true;
  rewardsError.hidden = true;
  const loading = document.getElementById('rewards-loading');
  loading.hidden = false;
  couponList.setAttribute('aria-busy', 'true');
  renderCoupons();
  const nextPage = reset ? 1 : couponPage + 1;
  try {
    const [pointsResponse, couponsResponse] = await Promise.all([
      customerRequest('me/points'),
      customerRequest(`privileges?page=${nextPage}`),
    ]);
    if (!pointsResponse.ok || !couponsResponse.ok) {
      if (pointsResponse.status === 401) throw new Error('Your membership is unavailable. Please contact the membership team.');
      throw new Error('We couldn’t refresh your points and coupons. Please try the refresh button.');
    }
    const [balance, items] = await Promise.all([pointsResponse.json(), couponsResponse.json()]);
    membership.customer.points = balance.points;
    balanceKnown = true;
    document.getElementById('member-points').textContent = balance.points.toLocaleString();
    const unresolved = Array.from(coupons.values()).filter((coupon) => pendingRedemptions.has(coupon.id));
    if (reset) coupons.clear();
    for (const coupon of items) coupons.set(coupon.id, coupon);
    for (const coupon of unresolved) {
      if (!coupons.has(coupon.id)) coupons.set(coupon.id, coupon);
    }
    couponPage = nextPage;
    moreCoupons = items.length === 100;
  } catch (failure) {
    balanceKnown = false;
    rewardsError.textContent = failure instanceof TypeError
      ? 'We couldn’t load your coupons. Check your connection and tap refresh to try again.'
      : failure.message;
    rewardsError.hidden = false;
  } finally {
    loadingRewards = false;
    loading.hidden = true;
    couponList.removeAttribute('aria-busy');
    renderCoupons();
    if (!rewardsError.hidden) document.getElementById('rewards-empty').hidden = true;
  }
}

async function redeemCoupon(coupon) {
  if (redeeming || loadingRewards) return;
  redeeming = true;
  redemptionStatus.hidden = false;
  redemptionStatus.classList.remove('error');
  redemptionStatus.textContent = `Redeeming ${coupon.name}…`;
  let refreshAfter = false;
  try {
    let key = pendingRedemptions.get(coupon.id);
    if (!key) {
      key = Array.from(crypto.getRandomValues(new Uint8Array(24)), (byte) => byte.toString(16).padStart(2, '0')).join('');
      pendingRedemptions.set(coupon.id, key);
    }
    renderCoupons();
    const response = await customerRequest('me/redemptions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key },
      body: JSON.stringify({ privilege_id: coupon.id }),
    });
    if (!response.ok) {
      if (response.status >= 400 && response.status < 500 && response.status !== 408 && response.status !== 429) {
        pendingRedemptions.delete(coupon.id);
        refreshAfter = true;
      }
      const messages = {
        401: 'Your membership is unavailable. Please contact the membership team.',
        404: 'This coupon is no longer available. Try another treat.',
        409: 'This coupon couldn’t be redeemed. Your points or its availability may have changed.',
        429: 'Please wait a moment, then retry your redemption.',
      };
      throw new Error(messages[response.status] || 'Your redemption couldn’t be confirmed. Retry this coupon to check it safely.');
    }
    const receipt = await response.json();
    pendingRedemptions.delete(coupon.id);
    balanceKnown = false;
    coupon.stock = Math.max(0, coupon.stock - 1);
    document.getElementById('member-points').textContent = '—';
    redemptionStatus.textContent = `${receipt.name} redeemed! ${receipt.cost.toLocaleString()} points spent. Receipt: ${receipt.id}. Keep this receipt for your records.`;
    refreshAfter = true;
  } catch (failure) {
    redemptionStatus.classList.add('error');
    redemptionStatus.textContent = failure instanceof TypeError
      ? 'We couldn’t confirm your redemption. Check your connection, then retry this coupon. Your retry will not spend points twice.'
      : failure.message;
  } finally {
    redeeming = false;
    renderCoupons();
  }
  if (refreshAfter) await loadRewards(true);
}

refreshButton.addEventListener('click', () => loadRewards(true));
moreButton.addEventListener('click', () => loadRewards(false));
// Personal member links keep the opaque member value in the fragment so it
// is not sent in page requests or referrers. Exchange it once for this browser.
async function openMemberPage() {
  const loading = document.getElementById('member-loading');
  const failure = document.getElementById('member-error');
  loading.hidden = false;
  try {
    const current = new URL(window.location.href);
    const member = new URLSearchParams(current.hash.slice(1)).get('member');
    let response;
    if (member) {
      response = await fetch(new URL('../api/v1/member-session', current), {
        method: 'POST', credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ member }),
      });
    } else {
      response = await customerRequest('me');
    }
    if (!response.ok) throw new Error('We couldn’t open your membership. Please ask the membership team for a new personal member link.');
    const result = await response.json();
    showMembership(member ? result.customer : result);
    if (member) window.history.replaceState(null, '', current.pathname + current.search);
    await loadRewards(true);
  } catch (error) {
    failure.textContent = error instanceof TypeError
      ? 'We couldn’t open your membership. Check your connection and try your member link again.'
      : error.message;
    failure.hidden = false;
  } finally {
    loading.hidden = true;
  }
}

if (new URL(window.location.href).pathname.endsWith('/coupons')) openMemberPage();
