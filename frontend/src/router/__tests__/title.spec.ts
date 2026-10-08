import { describe, expect, it } from 'vitest'
import { PURCHASE_ROUTE_NAME, resolveRouteMetaKeys } from '@/router/title'

describe('resolveRouteMetaKeys', () => {
  const purchaseRoute = {
    name: PURCHASE_ROUTE_NAME,
    meta: { titleKey: 'nav.buySubscription', descriptionKey: 'purchase.description' }
  }

  it('默认（充值 & 订阅或未知）沿用路由 meta 的标题/描述 key', () => {
    expect(resolveRouteMetaKeys(purchaseRoute)).toEqual({
      titleKey: 'nav.buySubscription',
      descriptionKey: 'purchase.description'
    })
    expect(resolveRouteMetaKeys(purchaseRoute, { billingMode: 'recharge_and_subscription' })).toEqual({
      titleKey: 'nav.buySubscription',
      descriptionKey: 'purchase.description'
    })
  })

  it('仅充值时 /purchase 切换为纯充值文案', () => {
    expect(resolveRouteMetaKeys(purchaseRoute, { billingMode: 'recharge_only' })).toEqual({
      titleKey: 'nav.recharge',
      descriptionKey: 'purchase.rechargeDescription'
    })
  })

  it('仅订阅时 /purchase 切换为纯订阅文案', () => {
    expect(resolveRouteMetaKeys(purchaseRoute, { billingMode: 'subscription_only' })).toEqual({
      titleKey: 'nav.subscribe',
      descriptionKey: 'purchase.subscriptionDescription'
    })
  })

  it('站点类型不影响其他路由', () => {
    const route = { name: 'Subscriptions', meta: { titleKey: 'userSubscriptions.title' } }
    expect(resolveRouteMetaKeys(route, { billingMode: 'recharge_only' })).toEqual({
      titleKey: 'userSubscriptions.title',
      descriptionKey: undefined
    })
  })
})
