export class MenuState {
  constructor(items) {
    this.items = items;
    this.index = 0;
  }

  up() {
    this.index = (this.index + this.items.length - 1) % this.items.length;
    return this.current();
  }

  down() {
    this.index = (this.index + 1) % this.items.length;
    return this.current();
  }

  pick(number) {
    const i = parseInt(number, 10);
    if (isNaN(i) || i < 1 || i > this.items.length) {
      return null;
    }
    this.index = i - 1;
    return this.current();
  }

  current() {
    return this.items[this.index];
  }

  currentIndex() {
    return this.index;
  }

  render() {
    const lines = this.items.map((item, i) => {
      const marker = i === this.index ? ">" : " ";
      return `${marker} ${i + 1}. ${item}`;
    });
    return lines.join("\n");
  }
}

export function posmod(a, b) {
  return ((a % b) + b) % b;
}
