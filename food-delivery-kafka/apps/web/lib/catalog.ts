import type { Restaurant } from './types';

/** Seed catalog (non-goal to model catalog CRUD — static data). */
export const CATALOG: Restaurant[] = [
  {
    id: 'r1', name: 'Meghana Foods', cuisines: ['Biryani', 'Andhra', 'North Indian'],
    rating: 4.5, etaMins: 32, priceForTwo: 500, offer: '50% OFF up to ₹100', emoji: '🍗',
    menu: [
      { category: 'Bestsellers', items: [
        { id: 'r1-1', name: 'Chicken Boneless Biryani', price: 320, veg: false, desc: 'Signature Andhra-style spicy biryani', bestseller: true },
        { id: 'r1-2', name: 'Paneer Biryani', price: 260, veg: true, desc: 'Fragrant basmati with paneer' },
      ]},
      { category: 'Starters', items: [
        { id: 'r1-3', name: 'Chicken 65', price: 240, veg: false, desc: 'Crispy fried chicken, deep red' },
        { id: 'r1-4', name: 'Gobi Manchurian', price: 180, veg: true, desc: 'Indo-Chinese cauliflower' },
      ]},
    ],
  },
  {
    id: 'r2', name: 'Truffles', cuisines: ['American', 'Burgers', 'Cafe'],
    rating: 4.6, etaMins: 28, priceForTwo: 600, offer: 'Free delivery', emoji: '🍔',
    menu: [
      { category: 'Burgers', items: [
        { id: 'r2-1', name: 'Ninja Burger', price: 280, veg: false, desc: 'Double chicken patty, cheese', bestseller: true },
        { id: 'r2-2', name: 'Veg Nirvana Burger', price: 240, veg: true, desc: 'Grilled veg patty' },
      ]},
      { category: 'Sides', items: [
        { id: 'r2-3', name: 'Peri Peri Fries', price: 150, veg: true, desc: 'Crispy fries, peri peri dust' },
      ]},
    ],
  },
  {
    id: 'r3', name: 'CTR - Central Tiffin Room', cuisines: ['South Indian', 'Breakfast'],
    rating: 4.7, etaMins: 22, priceForTwo: 200, offer: '20% OFF', emoji: '🥞',
    menu: [
      { category: 'Dosas', items: [
        { id: 'r3-1', name: 'Benne Masala Dosa', price: 120, veg: true, desc: 'Legendary butter dosa', bestseller: true },
        { id: 'r3-2', name: 'Rava Idli', price: 90, veg: true, desc: 'Soft semolina idli' },
      ]},
    ],
  },
  {
    id: 'r4', name: 'Empire Restaurant', cuisines: ['North Indian', 'Mughlai', 'Kebab'],
    rating: 4.3, etaMins: 38, priceForTwo: 450, offer: '₹125 OFF above ₹249', emoji: '🍛',
    menu: [
      { category: 'Mains', items: [
        { id: 'r4-1', name: 'Butter Chicken', price: 340, veg: false, desc: 'Creamy tomato gravy', bestseller: true },
        { id: 'r4-2', name: 'Paneer Butter Masala', price: 280, veg: true, desc: 'Rich makhani gravy' },
      ]},
      { category: 'Breads', items: [
        { id: 'r4-3', name: 'Butter Naan', price: 60, veg: true, desc: 'Tandoor-baked, buttered' },
      ]},
    ],
  },
  {
    id: 'r5', name: 'Pizza Bakery', cuisines: ['Pizza', 'Italian'],
    rating: 4.2, etaMins: 35, priceForTwo: 700, emoji: '🍕',
    menu: [
      { category: 'Pizzas', items: [
        { id: 'r5-1', name: 'Margherita', price: 300, veg: true, desc: 'San Marzano, fresh basil' },
        { id: 'r5-2', name: 'Pepperoni', price: 450, veg: false, desc: 'Loaded pepperoni', bestseller: true },
      ]},
    ],
  },
  {
    id: 'r6', name: 'Corner House', cuisines: ['Desserts', 'Ice Cream'],
    rating: 4.8, etaMins: 25, priceForTwo: 300, offer: 'Buy 1 Get 1', emoji: '🍨',
    menu: [
      { category: 'Sundaes', items: [
        { id: 'r6-1', name: 'Death by Chocolate', price: 260, veg: true, desc: 'Iconic DBC sundae', bestseller: true },
        { id: 'r6-2', name: 'Hot Chocolate Fudge', price: 240, veg: true, desc: 'Warm fudge, cold ice cream' },
      ]},
    ],
  },
];

export const findRestaurant = (id: string): Restaurant | undefined =>
  CATALOG.find((r) => r.id === id);
