// The same two loops for javac, so the ratio can be compared with a JIT.
//
// The class is named after the file, the way every other benchmark here is, and
// the way javac's rule about a public class forces: scripts/bench.sh compiles
// examples/NAME.java and then runs `java -cp /tmp NAME`, so a file whose public
// class is called something else produces no runnable NAME and its row quietly
// loses its Java column. That is exactly what had happened here.
import java.lang.reflect.*;

class Adder {
  private int base;

  public Adder(int base) {
    this.base = base;
  }
  public int add(int x) {
    return base + x;
  }
}

public class bench_invoke {
  public static void main(String[] args) throws Exception {
    int rounds = 20000000;
    Adder a = new Adder(7);
    Method add = Adder.class.getDeclaredMethod("add", int.class);
    Object[] box = new Object[1];

    long direct = 0;
    long t0 = System.nanoTime();
    for (int i = 0; i < rounds; i++) {
      direct += a.add(i);
    }
    long t1 = System.nanoTime();

    long reflected = 0;
    for (int i = 0; i < rounds; i++) {
      box[0] = i;
      reflected += (Integer) add.invoke(a, box);
    }
    long t2 = System.nanoTime();

    System.out.println("direct " + (t1 - t0) / 1000000 + " ms");
    System.out.println("invoke " + (t2 - t1) / 1000000 + " ms");
    System.out.println(direct == reflected);
  }
}
